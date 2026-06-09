// Package ssh implements a config-sync adapter that stores the
// encrypted blob on a remote host over the system `ssh` binary. It
// shells out rather than linking golang.org/x/crypto/ssh + a third-party
// SFTP library so it adds ZERO new dependencies and inherits the user's
// existing SSH config, agent, known_hosts, and key handling verbatim.
//
// The remote only ever receives the opaque AEAD ciphertext; the blob is
// written with mode 0600. Auth is whatever the user's ssh client is
// configured for (keys / agent) — jitenv never handles SSH credentials
// itself, so there is nothing secret to store in the sync sidecar for
// this adapter beyond the host/path.
package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

const typeName = "ssh"

// hostAllowed is a strict allowlist for the [user@]host argv value:
// letters, digits, and the small set of punctuation that legitimately
// appears in hosts and user@host forms. Combined with the explicit
// leading-'-' rejection below, this prevents a host that smuggles ssh
// options into the command line (argv flag injection).
var hostAllowed = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

func init() {
	syncadapters.Register(typeName, New)
}

// runner abstracts command execution so tests can inject a fake remote
// without a real SSH server.
type runner interface {
	// run executes the ssh binary with args, feeding stdin, and returns
	// stdout. A non-zero exit is returned as an error including stderr.
	run(ctx context.Context, stdin []byte, args ...string) (stdout []byte, err error)
}

type execRunner struct{ bin string }

func (e execRunner) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", err, msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

type adapter struct {
	host string // [user@]host (passed verbatim to ssh)
	path string // absolute remote path for the blob
	port string // optional
	r    runner
}

// New constructs an ssh adapter. Required params: "host" and "path".
// Optional: "port".
func New(cfg map[string]any) (syncadapter.Adapter, error) {
	host, _ := cfg["host"].(string)
	path, _ := cfg["path"].(string)
	port, _ := cfg["port"].(string)
	if host == "" {
		return nil, errors.New("ssh adapter: \"host\" param is required")
	}
	if path == "" {
		return nil, errors.New("ssh adapter: \"path\" param is required")
	}
	// Reject shell metacharacters in path/host: the remote path is
	// interpolated into a remote shell command, and host into argv.
	if strings.ContainsAny(path, "\"'$`;&|<>(){}*?\\\n\r") {
		return nil, fmt.Errorf("ssh adapter: remote path %q contains unsafe characters", path)
	}
	// A host beginning with '-' would be parsed by ssh/scp/sftp as an
	// option, not a hostname — reject it outright (argv flag smuggling).
	if strings.HasPrefix(host, "-") {
		return nil, errors.New("ssh adapter: host must not start with '-'")
	}
	// Strict allowlist (also bars whitespace/shell metacharacters).
	if !hostAllowed.MatchString(host) {
		return nil, fmt.Errorf("ssh adapter: host %q contains unsafe characters", host)
	}
	// Port, when set, must be purely numeric so it can never be parsed as
	// a flag or otherwise alter the ssh command line.
	if port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return nil, fmt.Errorf("ssh adapter: port %q is not numeric", port)
		}
	}
	return &adapter{host: host, path: path, port: port, r: execRunner{bin: "ssh"}}, nil
}

func (a *adapter) Name() string { return typeName }

func (a *adapter) baseArgs() []string {
	args := []string{"-o", "BatchMode=yes"}
	if a.port != "" {
		args = append(args, "-p", a.port)
	}
	// `--` ends option parsing: defence-in-depth so the host (already
	// allowlisted and barred from a leading '-') can never be treated as
	// an option even if validation were bypassed.
	args = append(args, "--")
	return append(args, a.host)
}

func (a *adapter) metaRemote() string { return a.path + ".meta.json" }

// Validate opens a non-interactive SSH session and confirms the parent
// directory of the blob path is writable.
func (a *adapter) Validate(ctx context.Context) error {
	dir := remoteDir(a.path)
	// `mkdir -p` (0700) then a write probe.
	remoteCmd := fmt.Sprintf("mkdir -p -m 700 %s && test -w %s", shQuote(dir), shQuote(dir))
	args := append(a.baseArgs(), remoteCmd)
	if _, err := a.r.run(ctx, nil, args...); err != nil {
		return fmt.Errorf("ssh adapter: validate %s: %w", a.host, err)
	}
	return nil
}

func (a *adapter) Push(ctx context.Context, blob []byte, meta syncadapter.Meta) error {
	if err := a.Validate(ctx); err != nil {
		return err
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := a.writeRemote(ctx, a.path, blob); err != nil {
		return fmt.Errorf("ssh adapter: write blob: %w", err)
	}
	if err := a.writeRemote(ctx, a.metaRemote(), mb); err != nil {
		return fmt.Errorf("ssh adapter: write meta: %w", err)
	}
	return nil
}

// writeRemote streams data to the remote path via `cat > tmp` then an
// atomic rename, and chmods 0600 so the ciphertext is never group/world
// readable even transiently.
func (a *adapter) writeRemote(ctx context.Context, remotePath string, data []byte) error {
	tmp := remotePath + ".jitenv-tmp"
	q := shQuote(remotePath)
	qt := shQuote(tmp)
	remoteCmd := fmt.Sprintf("umask 077 && cat > %s && chmod 600 %s && mv %s %s", qt, qt, qt, q)
	args := append(a.baseArgs(), remoteCmd)
	_, err := a.r.run(ctx, data, args...)
	return err
}

// Pull reads the (blob, meta) pair from the remote host. It
// distinguishes three outcomes (#279): both files absent →
// ErrNoRemoteState (clean first push); exactly one absent →
// ErrRemoteStateIncomplete (corrupt remote — typically a partial Push
// or a partial filesystem replication that delivered one file but not
// the other); both present → (blob, meta, nil).
//
// Conflating the two missing-cases let the engine's pre-push fence
// silently overwrite an orphan blob without --force; splitting them
// requires the user to opt into clobbering.
func (a *adapter) Pull(ctx context.Context) ([]byte, syncadapter.Meta, error) {
	blob, blobErr := a.readRemote(ctx, a.path)
	mb, metaErr := a.readRemote(ctx, a.metaRemote())

	blobMissing := errors.Is(blobErr, errRemoteMissing)
	metaMissing := errors.Is(metaErr, errRemoteMissing)

	switch {
	case blobMissing && metaMissing:
		return nil, syncadapter.Meta{}, syncadapters.ErrNoRemoteState
	case blobMissing && metaErr == nil:
		return nil, syncadapter.Meta{}, syncadapters.ErrRemoteStateIncomplete
	case metaMissing && blobErr == nil:
		return nil, syncadapter.Meta{}, syncadapters.ErrRemoteStateIncomplete
	}
	if blobErr != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("ssh adapter: read blob: %w", blobErr)
	}
	if metaErr != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("ssh adapter: read meta: %w", metaErr)
	}
	var meta syncadapter.Meta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("ssh adapter: parse meta: %w", err)
	}
	return blob, meta, nil
}

var errRemoteMissing = errors.New("remote file missing")

// readRemote cats a remote file. A missing file is reported as
// errRemoteMissing via a distinct shell exit sentinel.
func (a *adapter) readRemote(ctx context.Context, remotePath string) ([]byte, error) {
	q := shQuote(remotePath)
	// If the file is absent, exit 66 so we can tell "missing" apart from
	// a transport/auth failure.
	remoteCmd := fmt.Sprintf("if test -f %s; then cat %s; else exit 66; fi", q, q)
	args := append(a.baseArgs(), remoteCmd)
	out, err := a.r.run(ctx, nil, args...)
	if err != nil {
		if isExit(err, 66) {
			return nil, errRemoteMissing
		}
		return nil, err
	}
	return out, nil
}

// exitCoder is satisfied by *exec.ExitError (real ssh) and by test
// fakes, so the "remote file missing" sentinel (exit 66) can be detected
// without a live SSH server.
type exitCoder interface{ ExitCode() int }

func isExit(err error, code int) bool {
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode() == code
	}
	return false
}

// remoteDir returns the parent directory of an absolute POSIX path.
func remoteDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// shQuote single-quotes a string for safe use in a remote POSIX shell.
// New() already rejects characters that can't be made safe; this is
// defence in depth.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
