package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gv/jitenv/internal/lockfile"
)

// maxConcurrentAgentConns caps how many in-flight connection handlers
// the agent allows. A misbehaving (or hostile) same-user process could
// otherwise open arbitrarily many half-finished connections, each
// holding a goroutine and an fd. Excess connections are closed at
// accept time. Tunable from tests; the default is meant to sit well
// above any realistic workload.
var maxConcurrentAgentConns = 64

// shutdownDrainTimeout caps how long Shutdown waits for in-flight
// request handlers to complete before forcing exit. Tunable from tests.
var shutdownDrainTimeout = 2 * time.Second

// Agent is the per-user secret-fetching daemon.
type Agent struct {
	paths    Paths
	listener net.Listener
	// pidLock holds the exclusive flock/share-mode lock on the
	// pidfile for the agent's lifetime. Closing it releases the lock
	// and lets a future unlock take over without staleness checks
	// (security #130).
	pidLock *os.File

	startedAt time.Time
	hits      atomic.Int64
	lastSeen  atomic.Int64 // unix nanos of last request

	idleTimeout time.Duration

	cancel context.CancelFunc
	done   chan struct{}

	// handlers tracks in-flight connection goroutines so Shutdown can
	// drain them with a bounded timeout (security #134). Without this,
	// SIGTERM mid-fetch dropped a connection while the response was
	// half-written, surfacing as a confusing IPC error to the client.
	handlers sync.WaitGroup

	mu       sync.RWMutex
	resolver Resolver
	// reload, if set, rebuilds a Resolver from the current config on disk.
	// Called from the OpReload handler. Nil-safe.
	reload func() (Resolver, error)
}

// Resolver is the agent-internal hook to resolve mapping + fetch env vars.
type Resolver interface {
	Sources() []string
	IsMapped(absPath string) bool
	FetchEnv(ctx context.Context, absPath string) (map[string]string, error)

	// FetchEnvCwd serves the wrapper shim: env vars for (pwd, command).
	// Empty map when no cwd_glob mapping matches.
	FetchEnvCwd(ctx context.Context, pwd, command string) (map[string]string, error)
	// CwdCommands serves the chpwd helper: union of every cwd_glob
	// mapping's commands list whose pattern matches pwd.
	CwdCommands(pwd string) []string
}

// NewAgent constructs an Agent ready to Serve.
func NewAgent(paths Paths, idle time.Duration, r Resolver) *Agent {
	return &Agent{
		paths:       paths,
		startedAt:   time.Now(),
		idleTimeout: idle,
		resolver:    r,
	}
}

// SetReload installs a callback that rebuilds the Resolver from the
// current config on disk. The OpReload handler calls it; on success the
// new Resolver atomically replaces the current one.
func (a *Agent) SetReload(fn func() (Resolver, error)) {
	a.mu.Lock()
	a.reload = fn
	a.mu.Unlock()
}

func (a *Agent) currentResolver() Resolver {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.resolver
}

// Listen creates the agent transport endpoint. Caller must call Serve
// to accept.
//
// Also runs a one-time GC pass over ShellsDir, removing wrapper dirs
// for shell pids that aren't alive anymore. Cheap, idempotent.
//
// The actual binding step is platform-split into socket_unix.go (real
// net.ListenUnix at paths.Socket) and socket_windows.go (named-pipe
// listener at paths.Socket interpreted as a \\.\pipe\... name). The
// returned listener is just a net.Listener — per-connection peer
// authentication happens in checkPeerUid, which has its own
// platform-split implementations.
func (a *Agent) Listen() error {
	_ = GcOrphanShells(a.paths.ShellsDir)
	// OS-level advisory lock on a sibling `.lock` file (security #130).
	// We use a separate file rather than locking the pidfile itself so
	// the pidfile stays plain-readable/writable; on Windows, the
	// no-share CreateFile we use for the lock would otherwise block
	// WritePidFile's open of the same path with a sharing violation.
	// Both Unix flock and Windows share-mode locks auto-release on
	// process exit, so a crashed agent never leaves a stale lock.
	lock, err := lockfile.Acquire(a.paths.PidFile + ".lock")
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, _ := ReadPidFile(a.paths.PidFile)
			return fmt.Errorf("agent already running (pid %d)", existing)
		}
		return err
	}
	a.pidLock = lock
	// Best-effort cleanup of a stale socket file. No-op on Windows where
	// Socket is a pipe name, not a filesystem path.
	_ = os.Remove(a.paths.Socket)

	ln, err := listenSocket(a.paths.Socket)
	if err != nil {
		a.pidLock.Close()
		a.pidLock = nil
		return err
	}
	a.listener = ln
	return WritePidFile(a.paths.PidFile, os.Getpid())
}

// Serve runs the accept loop until ctx is cancelled or Shutdown is called.
func (a *Agent) Serve(ctx context.Context) error {
	if a.listener == nil {
		return errors.New("agent not listening")
	}
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})
	defer close(a.done)
	a.lastSeen.Store(time.Now().UnixNano())

	go a.idleLoop(ctx)

	go func() {
		<-ctx.Done()
		a.listener.Close()
	}()

	// Semaphore bounds concurrent connection handlers. Snapshotted at
	// Serve-time so tests can lower the cap before calling Serve.
	sem := make(chan struct{}, maxConcurrentAgentConns)

	for {
		conn, err := a.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case sem <- struct{}{}:
			a.handlers.Add(1)
			go func() {
				defer func() {
					<-sem
					a.handlers.Done()
				}()
				a.handle(ctx, conn)
			}()
		default:
			// Cap reached. Drop the conn rather than queue it — a
			// queue just delays the same DoS, and a legitimate
			// client will retry within milliseconds.
			_ = conn.Close()
		}
	}
}

// Shutdown stops accepting and removes socket + pidfile.
//
// Drains any in-flight request handlers for up to
// shutdownDrainTimeout so a SIGTERM / OpLock mid-fetch doesn't cut
// a half-written response off at the wire (security #134).
// Handlers that don't finish in time are abandoned — the OS will
// tear down the still-open conns when the process exits.
//
// Also asks the resolver to wipe any cached plaintext secret
// material so a memory dump immediately after shutdown contains
// fewer recoverable secrets (security #125). Go strings are
// immutable, so this isn't true zeroing — it drops live references
// so a future GC can reclaim the memory.
func (a *Agent) Shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.done != nil {
		<-a.done
	}
	drained := make(chan struct{})
	go func() {
		a.handlers.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(shutdownDrainTimeout):
	}
	_ = os.Remove(a.paths.Socket)
	_ = RemovePidFile(a.paths.PidFile)
	if a.pidLock != nil {
		_ = a.pidLock.Close()
		a.pidLock = nil
	}
	a.mu.Lock()
	if w, ok := a.resolver.(interface{ Wipe() }); ok {
		w.Wipe()
	}
	a.resolver = nil
	a.mu.Unlock()
}

func (a *Agent) idleLoop(ctx context.Context) {
	if a.idleTimeout <= 0 {
		return
	}
	t := time.NewTicker(time.Second * 30)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			last := time.Unix(0, a.lastSeen.Load())
			if time.Since(last) > a.idleTimeout {
				a.cancel()
				return
			}
		}
	}
}

// checkPeerUid lives in peer_linux.go / peer_darwin.go / peer_windows.go
// so each platform uses the right peer-credential mechanism: SO_PEERCRED
// on Linux, LOCAL_PEERCRED on Darwin, and GetNamedPipeClientProcessId +
// token-SID compare on Windows. The handler takes a net.Conn — each
// implementation type-asserts to the concrete connection type it expects
// (*net.UnixConn on Unix, the go-winio pipe conn on Windows).

func (a *Agent) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	// Set the per-conn deadline FIRST so even rejected (unauthorized)
	// clients can't hang the handler with a slow-read attack against
	// our WriteMessage. Previously the deadline was only applied after
	// the peer check, leaving the unauthorized-reply write unbounded
	// (security #114).
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = WriteMessage(c, Response{OK: false, Error: "set deadline: " + err.Error()})
		return
	}
	if err := checkPeerUid(c); err != nil {
		_ = WriteMessage(c, Response{OK: false, Error: "unauthorized"})
		return
	}

	var req Request
	if err := ReadMessage(c, &req); err != nil {
		_ = WriteMessage(c, Response{OK: false, Error: err.Error()})
		return
	}
	a.hits.Add(1)
	a.lastSeen.Store(time.Now().UnixNano())

	resp := a.dispatch(ctx, req)
	_ = WriteMessage(c, resp)

	if req.Op == OpLock {
		// Schedule shutdown after we've replied.
		go func() {
			time.Sleep(50 * time.Millisecond)
			a.cancel()
		}()
	}
}

func (a *Agent) dispatch(ctx context.Context, req Request) Response {
	switch req.Op {
	case OpStatus:
		r := a.currentResolver()
		var sources []string
		if r != nil {
			sources = r.Sources()
		}
		idle := time.Since(time.Unix(0, a.lastSeen.Load())).Truncate(time.Second).String()
		return Response{
			OK: true,
			Status: &Status{
				PID:       os.Getpid(),
				StartedAt: a.startedAt.Format(time.RFC3339),
				Sources:   sources,
				Hits:      a.hits.Load(),
				IdleFor:   idle,
			},
		}
	case OpLock:
		return Response{OK: true}
	case OpIsMapped:
		r := a.currentResolver()
		if r == nil {
			return Response{OK: true, Mapped: false}
		}
		return Response{OK: true, Mapped: r.IsMapped(req.Path)}
	case OpFetchEnv:
		r := a.currentResolver()
		if r == nil {
			return Response{OK: true, Env: map[string]string{}}
		}
		env, err := r.FetchEnv(ctx, req.Path)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, Env: env}
	case OpFetchEnvCwd:
		r := a.currentResolver()
		if r == nil {
			return Response{OK: true, Env: map[string]string{}}
		}
		env, err := r.FetchEnvCwd(ctx, req.Cwd, req.Command)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, Env: env}
	case OpCwdCommands:
		r := a.currentResolver()
		if r == nil {
			return Response{OK: true, Commands: nil}
		}
		return Response{OK: true, Commands: r.CwdCommands(req.Cwd)}
	case OpReload:
		a.mu.Lock()
		fn := a.reload
		a.mu.Unlock()
		if fn == nil {
			return Response{OK: false, Error: "reload not supported"}
		}
		next, err := fn()
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		// Swap first, then Wipe the outgoing resolver. This mirrors
		// the Shutdown path (server.go ~L229) and bounds the
		// in-memory copies of decrypted secret material to one:
		// without Wipe-on-swap, every TUI save / sync pull stacks
		// another resolver's worth of plaintext bag values + decrypted
		// SourceConfig params on the heap until GC pages them out
		// (security #287). The Wipe runs AFTER the swap so any
		// concurrent FetchEnv requests in flight against the old
		// resolver finish against fully-populated state — they hold
		// their own reference via currentResolver().
		a.mu.Lock()
		old := a.resolver
		a.resolver = next
		a.mu.Unlock()
		if w, ok := old.(interface{ Wipe() }); ok {
			w.Wipe()
		}
		return Response{OK: true}
	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// AwaitSignal returns a context that cancels on SIGTERM/SIGINT.
func AwaitSignal(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signalNotify(ch)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func signalNotify(ch chan os.Signal) {
	// indirect to keep this file from importing os/signal at top level for tests
	signalRegister(ch, syscall.SIGINT, syscall.SIGTERM)
}
