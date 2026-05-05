package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/gv/jitenv/internal/crypto"
)

// SpawnDaemon re-execs the current binary as a detached agent process,
// passing the derived key over an inherited pipe. It returns once the
// child is running (socket present) or with an error if startup fails.
//
// configFile and idle are forwarded so the child loads the same config
// the parent verified.
func SpawnDaemon(paths Paths, configFile string, idle time.Duration, key []byte) error {
	if existing, _ := ReadPidFile(paths.PidFile); existing > 0 && PidAlive(existing) {
		return fmt.Errorf("agent already running (pid %d)", existing)
	}
	if len(key) != int(crypto.KeyLen) {
		return errors.New("daemonize: invalid key length")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pw.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logF, err := os.OpenFile(paths.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logF.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	args := []string{
		"__agent",
		"--key-fd=3",
		"--config=" + configFile,
		"--idle=" + idle.String(),
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = devNull
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.ExtraFiles = []*os.File{pr} // becomes fd 3 in child
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		pr.Close()
		return err
	}
	pr.Close() // child holds its own copy

	if _, err := pw.Write(key); err != nil {
		return fmt.Errorf("write key to child: %w", err)
	}
	pw.Close()

	// Wait for the socket to appear, indicating Listen succeeded.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.Socket); err == nil {
			// Detach from the child completely.
			_ = cmd.Process.Release()
			return nil
		}
		// If the child died early, surface that.
		if cmd.ProcessState != nil {
			return fmt.Errorf("agent exited early: %s", cmd.ProcessState)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return errors.New("agent did not start within 3s; check ${XDG_RUNTIME_DIR}/jitenv/agent.log")
}

// ReadKeyFromFd reads exactly KeyLen bytes from the given file descriptor.
// The caller is responsible for zeroing the returned slice.
func ReadKeyFromFd(fd int) ([]byte, error) {
	f := os.NewFile(uintptr(fd), "key-pipe")
	if f == nil {
		return nil, fmt.Errorf("fd %d not available", fd)
	}
	defer f.Close()
	buf := make([]byte, crypto.KeyLen)
	if _, err := readFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			if total == len(buf) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}
