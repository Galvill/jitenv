package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes a Scenario against a docker-compose stack.
type Runner struct {
	Scenario     *Scenario
	ScenarioPath string
	RunDir       string
	ComposeFile  string
	Project      string
	Out          io.Writer

	// DefaultTimeout is the per-step timeout when the step does not
	// set its own. Eight minutes is enough for `go build` from cold
	// inside an alpine container; trim later if it bites.
	DefaultTimeout time.Duration

	// lastExec is the index of the most recent exec step. assert_*
	// steps without an explicit Target refer to this one.
	lastExec int
	// stepDirs maps step name → run-dir subpath, so a Target lookup
	// can read back stdout/stderr/exit.
	stepDirs map[string]string
}

// Report is what the runner writes to <run>/summary.json. Everything
// the calling agent needs to triage a failure should be referenced here
// or live alongside it in the run dir.
type Report struct {
	Scenario  string       `json:"scenario"`
	Service   string       `json:"service"`
	Verdict   string       `json:"verdict"` // "pass" | "fail"
	StartedAt time.Time    `json:"started_at"`
	EndedAt   time.Time    `json:"ended_at"`
	Steps     []StepReport `json:"steps"`
	Failure   string       `json:"failure,omitempty"`
}

// StepReport is one entry in Report.Steps. The Dir field is the
// per-step subdirectory (relative to the run dir); the harness writes
// cmd / stdout / stderr / exit there.
type StepReport struct {
	Index    int           `json:"index"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind"` // "exec" | "wait_for_file" | "assert_*"
	Dir      string        `json:"dir"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration_ns"`
	Error    string        `json:"error,omitempty"`
	Status   string        `json:"status"` // "ok" | "fail" | "skipped"
}

// Run executes every step and returns a Report. The runner does not
// stop on the first failure: subsequent steps still run so the run dir
// captures as much state as possible. Verdict is "fail" if any step
// failed.
func (r *Runner) Run() *Report {
	if r.DefaultTimeout == 0 {
		r.DefaultTimeout = 8 * time.Minute
	}
	r.stepDirs = map[string]string{}
	r.lastExec = -1

	rep := &Report{
		Scenario:  r.Scenario.Name,
		Service:   r.Scenario.Service,
		StartedAt: time.Now().UTC(),
	}
	r.logf("=== scenario %s on service %s ===\n", r.Scenario.Name, r.Scenario.Service)

	for i, st := range r.Scenario.Steps {
		sr := r.runStep(i, &st)
		rep.Steps = append(rep.Steps, sr)
		r.logf("[%02d] %s (%s) → %s\n", sr.Index, sr.Name, sr.Kind, sr.Status)
		if sr.Status == "fail" && rep.Failure == "" {
			rep.Failure = fmt.Sprintf("step %d (%s): %s", sr.Index, sr.Name, sr.Error)
		}
	}

	rep.EndedAt = time.Now().UTC()
	rep.Verdict = "pass"
	for _, s := range rep.Steps {
		if s.Status == "fail" {
			rep.Verdict = "fail"
			break
		}
	}
	r.captureTeardown()
	r.logf("=== verdict: %s ===\n", rep.Verdict)
	return rep
}

// runStep dispatches one step. The step writes its own per-step
// directory (the only files the harness writes per step are cmd,
// stdout, stderr, exit, plus a meta.json for non-exec steps).
func (r *Runner) runStep(i int, st *Step) StepReport {
	stepDirName := fmt.Sprintf("%03d-%s", i, sanitize(st.Name))
	dirAbs := filepath.Join(r.RunDir, "steps", stepDirName)
	if err := os.MkdirAll(dirAbs, 0755); err != nil {
		return StepReport{Index: i, Name: st.Name, Kind: "unknown", Status: "fail", Error: err.Error()}
	}
	r.stepDirs[st.Name] = dirAbs

	start := time.Now()
	sr := StepReport{Index: i, Name: st.Name, Dir: filepath.Join("steps", stepDirName)}

	switch {
	case st.Exec != "":
		sr.Kind = "exec"
		r.lastExec = i
		ec, err := r.doExec(st, dirAbs)
		sr.ExitCode = ec
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.WaitForFile != "":
		sr.Kind = "wait_for_file"
		err := r.doWaitForFile(st, dirAbs)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.AssertExitCode != nil:
		sr.Kind = "assert_exit_code"
		err := r.doAssertExit(*st.AssertExitCode, st.Target, dirAbs)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.AssertStdoutContains != "":
		sr.Kind = "assert_stdout_contains"
		err := r.doAssertStream("stdout", st.AssertStdoutContains, st.Target, dirAbs, contains)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.AssertStdoutEquals != "":
		sr.Kind = "assert_stdout_equals"
		err := r.doAssertStream("stdout", st.AssertStdoutEquals, st.Target, dirAbs, equals)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.AssertStderrContains != "":
		sr.Kind = "assert_stderr_contains"
		err := r.doAssertStream("stderr", st.AssertStderrContains, st.Target, dirAbs, contains)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	case st.AssertStdoutNotContain != "":
		sr.Kind = "assert_stdout_not_contains"
		err := r.doAssertStream("stdout", st.AssertStdoutNotContain, st.Target, dirAbs, notContains)
		if err != nil {
			sr.Status = "fail"
			sr.Error = err.Error()
		} else {
			sr.Status = "ok"
		}
	default:
		sr.Kind = "unknown"
		sr.Status = "fail"
		sr.Error = "no action"
	}

	sr.Duration = time.Since(start)
	return sr
}

// doExec runs an `exec` step: docker compose exec --user <u> <svc> bash -c <cmd>.
// stdout/stderr are tee'd into the per-step dir AND the runner's Out.
func (r *Runner) doExec(st *Step, dirAbs string) (int, error) {
	user := r.Scenario.User
	if st.User != "" {
		user = st.User
	}
	service := r.Scenario.Service
	if st.Service != "" {
		service = st.Service
	}

	composeArgs := []string{"compose", "-f", r.ComposeFile, "-p", r.Project, "exec", "-T", "--user", user}
	for k, v := range st.Env {
		composeArgs = append(composeArgs, "-e", k+"="+v)
	}
	composeArgs = append(composeArgs, service, "bash", "-c", st.Exec)

	if err := os.WriteFile(filepath.Join(dirAbs, "cmd"), []byte(st.Exec+"\n"), 0644); err != nil {
		return -1, err
	}

	timeout := r.DefaultTimeout
	if st.Timeout != "" {
		if d, err := time.ParseDuration(st.Timeout); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	if st.Stdin != "" {
		cmd.Stdin = strings.NewReader(st.Stdin)
	}

	stdoutF, err := os.Create(filepath.Join(dirAbs, "stdout"))
	if err != nil {
		return -1, err
	}
	defer stdoutF.Close()
	stderrF, err := os.Create(filepath.Join(dirAbs, "stderr"))
	if err != nil {
		return -1, err
	}
	defer stderrF.Close()

	// Tee through the runner's Out so a human running this manually can
	// follow along; the per-step files are the source of truth for
	// later assertions.
	cmd.Stdout = io.MultiWriter(stdoutF, prefixWriter{w: r.Out, prefix: "    | "})
	cmd.Stderr = io.MultiWriter(stderrF, prefixWriter{w: r.Out, prefix: "    ! "})

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			err = nil // exit codes aren't runner failures
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = -1
			err = fmt.Errorf("timeout after %s", timeout)
		} else {
			exitCode = -1
		}
	}
	if writeErr := os.WriteFile(filepath.Join(dirAbs, "exit"), []byte(fmt.Sprintf("%d\n", exitCode)), 0644); writeErr != nil && err == nil {
		err = writeErr
	}
	return exitCode, err
}

// doWaitForFile polls inside the service until the file exists. It
// uses bash inside the container so we don't need a host-side mount.
func (r *Runner) doWaitForFile(st *Step, dirAbs string) error {
	timeout := 30 * time.Second
	if st.Timeout != "" {
		if d, err := time.ParseDuration(st.Timeout); err == nil {
			timeout = d
		}
	}
	deadline := time.Now().Add(timeout)
	user := r.Scenario.User
	service := r.Scenario.Service

	if err := os.WriteFile(filepath.Join(dirAbs, "cmd"), []byte("wait_for_file: "+st.WaitForFile+"\n"), 0644); err != nil {
		return err
	}

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "compose", "-f", r.ComposeFile, "-p", r.Project,
			"exec", "-T", "--user", user, service, "test", "-e", st.WaitForFile)
		err := cmd.Run()
		cancel()
		if err == nil {
			_ = os.WriteFile(filepath.Join(dirAbs, "exit"), []byte("0\n"), 0644)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = os.WriteFile(filepath.Join(dirAbs, "exit"), []byte("1\n"), 0644)
	return fmt.Errorf("file %q did not appear within %s", st.WaitForFile, timeout)
}

// doAssertExit reads the exit file of a previous exec step and checks
// it against want.
func (r *Runner) doAssertExit(want int, target, dirAbs string) error {
	t, err := r.targetExitCode(target)
	if err != nil {
		return err
	}
	r.writeAssertMeta(dirAbs, fmt.Sprintf("want_exit=%d got_exit=%d target=%s", want, t, r.targetName(target)))
	if t != want {
		return fmt.Errorf("expected exit %d, got %d (target=%s)", want, t, r.targetName(target))
	}
	return nil
}

type matchFn func(haystack, needle string) (bool, string)

func contains(h, n string) (bool, string) {
	if strings.Contains(h, n) {
		return true, ""
	}
	return false, fmt.Sprintf("expected to contain %q", n)
}

func equals(h, n string) (bool, string) {
	h = strings.TrimRight(h, "\n")
	if h == n {
		return true, ""
	}
	return false, fmt.Sprintf("expected %q, got %q", n, h)
}

func notContains(h, n string) (bool, string) {
	if !strings.Contains(h, n) {
		return true, ""
	}
	return false, fmt.Sprintf("expected NOT to contain %q (but it did)", n)
}

func (r *Runner) doAssertStream(stream, want, target, dirAbs string, fn matchFn) error {
	body, err := r.targetStream(target, stream)
	if err != nil {
		return err
	}
	r.writeAssertMeta(dirAbs, fmt.Sprintf("stream=%s want=%q target=%s", stream, want, r.targetName(target)))
	ok, msg := fn(body, want)
	if !ok {
		return fmt.Errorf("%s (target=%s, stream=%s); have: %q",
			msg, r.targetName(target), stream, truncate(body, 240))
	}
	return nil
}

// targetExitCode resolves the step name (or last exec) and reads its
// exit file from the run dir.
func (r *Runner) targetExitCode(name string) (int, error) {
	dir, err := r.targetDir(name)
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "exit"))
	if err != nil {
		return 0, fmt.Errorf("read exit: %w", err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &n); err != nil {
		return 0, fmt.Errorf("parse exit %q: %w", string(b), err)
	}
	return n, nil
}

func (r *Runner) targetStream(name, stream string) (string, error) {
	dir, err := r.targetDir(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, stream))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", stream, err)
	}
	return string(b), nil
}

func (r *Runner) targetDir(name string) (string, error) {
	if name != "" {
		d, ok := r.stepDirs[name]
		if !ok {
			return "", fmt.Errorf("target step %q not found", name)
		}
		return d, nil
	}
	if r.lastExec < 0 {
		return "", fmt.Errorf("assert_* with no target and no preceding exec")
	}
	last := r.Scenario.Steps[r.lastExec]
	d, ok := r.stepDirs[last.Name]
	if !ok {
		return "", fmt.Errorf("internal: last exec dir not registered")
	}
	return d, nil
}

func (r *Runner) targetName(name string) string {
	if name != "" {
		return name
	}
	if r.lastExec < 0 {
		return "(none)"
	}
	return r.Scenario.Steps[r.lastExec].Name
}

func (r *Runner) writeAssertMeta(dirAbs, line string) {
	_ = os.WriteFile(filepath.Join(dirAbs, "cmd"), []byte(line+"\n"), 0644)
}

// captureTeardown collects post-run state into the run dir: a tail of
// the agent log, the current config.toml (encrypted), and a compose
// ps snapshot. Each is best-effort; failures are noted in a per-file
// .err but don't fail the run.
func (r *Runner) captureTeardown() {
	tdDir := filepath.Join(r.RunDir, "teardown")
	_ = os.MkdirAll(tdDir, 0755)

	r.collect(tdDir, "compose-ps.txt",
		"docker", "compose", "-f", r.ComposeFile, "-p", r.Project, "ps")
	// Best-effort grab of jitenv runtime state. The container's runtime
	// dir is /run/user/1000/jitenv (XDG) or /tmp/jitenv-<uid>; we try
	// both via shell.
	r.collect(tdDir, "agent.log",
		"docker", "compose", "-f", r.ComposeFile, "-p", r.Project,
		"exec", "-T", "--user", r.Scenario.User, r.Scenario.Service,
		"bash", "-c", `cat "$XDG_RUNTIME_DIR/jitenv/agent.log" 2>/dev/null || cat "/tmp/jitenv-$UID/agent.log" 2>/dev/null || echo "(no agent.log)"`)
	r.collect(tdDir, "config.toml",
		"docker", "compose", "-f", r.ComposeFile, "-p", r.Project,
		"exec", "-T", "--user", r.Scenario.User, r.Scenario.Service,
		"bash", "-c", `cat "${JITENV_CONFIG:-$HOME/.config/jitenv/config.toml}" 2>/dev/null || echo "(no config.toml)"`)
	r.collect(tdDir, "ps-aux.txt",
		"docker", "compose", "-f", r.ComposeFile, "-p", r.Project,
		"exec", "-T", "--user", r.Scenario.User, r.Scenario.Service,
		"bash", "-c", "ps -ef")
}

func (r *Runner) collect(dir, name string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	dst := filepath.Join(dir, name)
	if writeErr := os.WriteFile(dst, out, 0644); writeErr != nil {
		_ = os.WriteFile(dst+".err", []byte(writeErr.Error()), 0644)
	}
	if err != nil {
		_ = os.WriteFile(dst+".err", []byte(err.Error()+"\n"), 0644)
	}
}

// Write serialises the report to summary.json + meta.json. Keep them
// separate so a calling agent can read meta without parsing all step
// detail.
func (rep *Report) Write(runDir string) error {
	full, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "summary.json"), full, 0644); err != nil {
		return err
	}
	meta := map[string]any{
		"scenario":   rep.Scenario,
		"service":    rep.Service,
		"verdict":    rep.Verdict,
		"started_at": rep.StartedAt,
		"ended_at":   rep.EndedAt,
		"failure":    rep.Failure,
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(runDir, "meta.json"), mb, 0644)
}

func (r *Runner) logf(format string, args ...any) {
	if r.Out == nil {
		return
	}
	fmt.Fprintf(r.Out, format, args...)
}

// sanitize strips characters that shouldn't appear in directory names.
// We don't need to be exhaustive — scenario authors choose step names.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		case c == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "step"
	}
	return string(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (p prefixWriter) Write(b []byte) (int, error) {
	if p.w == nil {
		return len(b), nil
	}
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if line == "" {
			continue
		}
		if _, err := io.WriteString(p.w, p.prefix+line); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}
