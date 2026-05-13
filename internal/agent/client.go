package agent

import (
	"context"
	"fmt"
	"time"
)

// Client talks to a running agent over its per-user transport. On
// Unix the socketPath is a filesystem path bound to an AF_UNIX socket;
// on Windows it is a named-pipe name (e.g. \\.\pipe\jitenv-<sid>). The
// caller doesn't need to care — dialAgent is platform-split.
type Client struct {
	socketPath string
	timeout    time.Duration
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 10 * time.Second}
}

func (c *Client) call(ctx context.Context, req Request) (Response, error) {
	conn, err := dialAgent(ctx, c.socketPath, c.timeout)
	if err != nil {
		return Response{}, fmt.Errorf("connect agent: %w", err)
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return Response{}, fmt.Errorf("set deadline: %w", err)
	}

	if err := WriteMessage(conn, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := ReadMessage(conn, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, fmt.Errorf("agent: %s", resp.Error)
	}
	return resp, nil
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	r, err := c.call(ctx, Request{Op: OpStatus})
	if err != nil {
		return nil, err
	}
	return r.Status, nil
}

func (c *Client) Lock(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpLock})
	return err
}

func (c *Client) IsMapped(ctx context.Context, path string) (bool, error) {
	r, err := c.call(ctx, Request{Op: OpIsMapped, Path: path})
	if err != nil {
		return false, err
	}
	return r.Mapped, nil
}

func (c *Client) FetchEnv(ctx context.Context, path string) (map[string]string, error) {
	r, err := c.call(ctx, Request{Op: OpFetchEnv, Path: path})
	if err != nil {
		return nil, err
	}
	return r.Env, nil
}

// FetchEnvCwd is the cwd_glob counterpart to FetchEnv. Used by the
// wrapper-symlink shim.
func (c *Client) FetchEnvCwd(ctx context.Context, pwd, command string) (map[string]string, error) {
	r, err := c.call(ctx, Request{Op: OpFetchEnvCwd, Cwd: pwd, Command: command})
	if err != nil {
		return nil, err
	}
	return r.Env, nil
}

// CwdCommands asks the agent which command names should be wrapped at
// pwd. Returns nil when no cwd_glob mapping matches.
func (c *Client) CwdCommands(ctx context.Context, pwd string) ([]string, error) {
	r, err := c.call(ctx, Request{Op: OpCwdCommands, Cwd: pwd})
	if err != nil {
		return nil, err
	}
	return r.Commands, nil
}

// Reload asks the agent to re-read the config file from disk and rebuild
// its resolver. Used by the TUI after a successful save.
func (c *Client) Reload(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpReload})
	return err
}
