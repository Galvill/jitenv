package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Op string

const (
	OpStatus      Op = "status"
	OpIsMapped    Op = "is_mapped"
	OpFetchEnv    Op = "fetch_env"
	OpLock        Op = "lock"
	OpReload      Op = "reload"
	OpFetchEnvCwd Op = "fetch_env_cwd" // shim → agent: env vars for (cwd, command)
	OpCwdCommands Op = "cwd_commands"  // chpwd helper → agent: list of commands wrapped at this cwd
)

type Request struct {
	Op      Op     `json:"op"`
	Path    string `json:"path,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command,omitempty"`
}

type Response struct {
	OK       bool              `json:"ok"`
	Error    string            `json:"error,omitempty"`
	Mapped   bool              `json:"mapped,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Status   *Status           `json:"status,omitempty"`
	Commands []string          `json:"commands,omitempty"`
}

type Status struct {
	PID       int      `json:"pid"`
	StartedAt string   `json:"started_at"`
	Sources   []string `json:"sources"`
	Hits      int64    `json:"hits"`
	IdleFor   string   `json:"idle_for,omitempty"`
}

const maxMessageBytes = 1 << 20 // 1 MiB

// WriteMessage writes a length-prefixed JSON message.
func WriteMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxMessageBytes {
		return fmt.Errorf("message too large: %d", len(b))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadMessage reads a length-prefixed JSON message into v.
func ReadMessage(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return errors.New("empty message")
	}
	if n > maxMessageBytes {
		return fmt.Errorf("message too large: %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
