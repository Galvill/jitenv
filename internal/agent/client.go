package agent

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Client talks to a running agent over its unix socket.
type Client struct {
	socketPath string
	timeout    time.Duration
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 10 * time.Second}
}

func (c *Client) call(ctx context.Context, req Request) (Response, error) {
	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connect agent: %w", err)
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	conn.SetDeadline(deadline)

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

// Reload asks the agent to re-read the config file from disk and rebuild
// its resolver. Used by the TUI after a successful save.
func (c *Client) Reload(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpReload})
	return err
}
