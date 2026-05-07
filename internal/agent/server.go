package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Agent is the per-user secret-fetching daemon.
type Agent struct {
	paths    Paths
	listener *net.UnixListener

	startedAt time.Time
	hits      atomic.Int64
	lastSeen  atomic.Int64 // unix nanos of last request

	idleTimeout time.Duration

	cancel context.CancelFunc
	done   chan struct{}

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

	// HasCwdMappings reports whether any cwd_glob mapping is
	// configured. The agent uses this to maintain the has-cwd
	// sentinel file the shell hooks check.
	HasCwdMappings() bool
	IsMappedCwd(pwd, command string) bool
	FetchEnvCwd(ctx context.Context, pwd, command string) (map[string]string, error)
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

// Listen creates the unix socket. Caller must call Serve to accept.
func (a *Agent) Listen() error {
	if existing, _ := ReadPidFile(a.paths.PidFile); existing > 0 && PidAlive(existing) {
		return fmt.Errorf("agent already running (pid %d)", existing)
	}
	_ = os.Remove(a.paths.Socket)

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: a.paths.Socket, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.paths.Socket, err)
	}
	if err := os.Chmod(a.paths.Socket, 0600); err != nil {
		ln.Close()
		return err
	}
	a.listener = ln
	a.refreshCwdSentinel()
	return WritePidFile(a.paths.PidFile, os.Getpid())
}

// refreshCwdSentinel writes (or removes) the has-cwd file that the
// shell hooks use to decide whether to pay the cost of asking the
// agent on every bare-PATH command. Called from Listen and after a
// successful Reload; safe to call concurrently because it only
// writes/removes one file in the per-user runtime dir.
func (a *Agent) refreshCwdSentinel() {
	flag := filepath.Join(a.paths.Dir, "has-cwd")
	r := a.currentResolver()
	if r != nil && r.HasCwdMappings() {
		_ = os.WriteFile(flag, nil, 0600)
		return
	}
	_ = os.Remove(flag)
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

	for {
		conn, err := a.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go a.handle(ctx, conn)
	}
}

// Shutdown stops accepting and removes socket + pidfile.
func (a *Agent) Shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.done != nil {
		<-a.done
	}
	_ = os.Remove(a.paths.Socket)
	_ = RemovePidFile(a.paths.PidFile)
	_ = os.Remove(filepath.Join(a.paths.Dir, "has-cwd"))
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

// checkPeerUid enforces that the connecting client runs as the same uid.
func checkPeerUid(c *net.UnixConn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var ucred *unix.Ucred
	var sysErr error
	err = raw.Control(func(fd uintptr) {
		ucred, sysErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if sysErr != nil {
		return sysErr
	}
	if int(ucred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d != %d", ucred.Uid, os.Getuid())
	}
	return nil
}

func (a *Agent) handle(ctx context.Context, c *net.UnixConn) {
	defer c.Close()
	if err := checkPeerUid(c); err != nil {
		_ = WriteMessage(c, Response{OK: false, Error: "unauthorized"})
		return
	}
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = WriteMessage(c, Response{OK: false, Error: "set deadline: " + err.Error()})
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
		if req.Cwd != "" {
			return Response{OK: true, Mapped: r.IsMappedCwd(req.Cwd, req.Command)}
		}
		return Response{OK: true, Mapped: r.IsMapped(req.Path)}
	case OpFetchEnv:
		r := a.currentResolver()
		if r == nil {
			return Response{OK: true, Env: map[string]string{}}
		}
		var (
			env map[string]string
			err error
		)
		if req.Cwd != "" {
			env, err = r.FetchEnvCwd(ctx, req.Cwd, req.Command)
		} else {
			env, err = r.FetchEnv(ctx, req.Path)
		}
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, Env: env}
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
		a.mu.Lock()
		a.resolver = next
		a.mu.Unlock()
		a.refreshCwdSentinel()
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
