package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

const Version = 1

type Config struct {
	Version  int                          `toml:"version"`
	Meta     Meta                         `toml:"_meta"`
	Agent    AgentConfig                  `toml:"agent"`
	Sources  map[string]SourceConfig      `toml:"sources"`
	Mappings []Mapping                    `toml:"mappings"`
	Secrets  map[string]map[string]string `toml:"secrets,omitempty"`
}

// Meta carries KDF parameters and a passphrase verification sentinel.
// All fields are plaintext on disk; "Verify" is an enc:v1: blob whose
// successful decryption proves the passphrase.
type Meta struct {
	KDF            string `toml:"kdf"`
	ArgonTime      uint32 `toml:"argon_time"`
	ArgonMemoryKiB uint32 `toml:"argon_memory_kib"`
	ArgonThreads   uint8  `toml:"argon_threads"`
	Salt           string `toml:"salt"`   // base64
	Verify         string `toml:"verify"` // enc:v1:...
}

type AgentConfig struct {
	IdleTimeout string `toml:"idle_timeout,omitempty"`
	SocketPath  string `toml:"socket_path,omitempty"`
	// PreRunNotice is *bool so a missing key in config.toml is
	// distinguishable from an explicit "false". Missing → use the
	// default-on behaviour exposed by PreRunNoticeEnabled. Read access
	// should always go through that helper.
	PreRunNotice *bool `toml:"pre_run_notice,omitempty"`
}

// PreRunNoticeEnabled reports whether the "jitenv: injected N
// variable(s)" stderr line should be printed before mapped commands
// exec. The notice is on by default; the flag exists so users who
// prefer the silent UX can opt out via TUI Settings.
func (a AgentConfig) PreRunNoticeEnabled() bool {
	if a.PreRunNotice == nil {
		return true
	}
	return *a.PreRunNotice
}

type SourceConfig struct {
	Type   string         `toml:"type"`
	Params map[string]any `toml:"params,omitempty"`
}

type Mapping struct {
	Path     string   `toml:"path,omitempty"`
	Glob     string   `toml:"glob,omitempty"`
	CwdGlob  string   `toml:"cwd_glob,omitempty"`
	Commands []string `toml:"commands,omitempty"` // required when CwdGlob is set
	Vars     []VarRef `toml:"vars"`
}

// Kind reports which match-shape a mapping uses.
func (m Mapping) Kind() string {
	switch {
	case m.Path != "":
		return "path"
	case m.Glob != "":
		return "glob"
	case m.CwdGlob != "":
		return "cwd"
	}
	return ""
}

type VarRef struct {
	Name   string            `toml:"name"`
	Source string            `toml:"source"`
	Ref    string            `toml:"ref,omitempty"`
	Key    string            `toml:"key,omitempty"`
	Extra  map[string]string `toml:"extra,omitempty"`
}

// Load reads and parses a config file. It does not decrypt envelopes.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if _, err := toml.Decode(string(b), &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Version == 0 {
		c.Version = Version
	}
	return &c, nil
}

// Save writes the config to path with 0600 permissions.
func Save(path string, c *Config) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(c)
}

// Validate performs structural checks not covered by TOML parsing.
func (c *Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("unsupported config version %d (want %d)", c.Version, Version)
	}
	for name, s := range c.Sources {
		if s.Type == "" {
			return fmt.Errorf("source %q: missing type", name)
		}
		if s.Type == "github" {
			return fmt.Errorf("source %q: unknown source type %q (the github backend was removed; remove this entry from your config)", name, s.Type)
		}
	}
	for i, m := range c.Mappings {
		set := 0
		if m.Path != "" {
			set++
		}
		if m.Glob != "" {
			set++
		}
		if m.CwdGlob != "" {
			set++
		}
		if set != 1 {
			return fmt.Errorf("mapping[%d]: exactly one of path, glob, or cwd_glob is required", i)
		}
		if m.CwdGlob == "" && len(m.Commands) > 0 {
			return fmt.Errorf("mapping[%d]: commands is only valid with cwd_glob", i)
		}
		if m.CwdGlob != "" {
			if len(m.Commands) == 0 {
				return fmt.Errorf("mapping[%d]: cwd_glob requires a non-empty commands list", i)
			}
			for j, cmd := range m.Commands {
				if cmd == "" {
					return fmt.Errorf("mapping[%d].commands[%d]: empty command name", i, j)
				}
			}
		}
		if len(m.Vars) == 0 {
			return fmt.Errorf("mapping[%d]: at least one var is required", i)
		}
		for j, v := range m.Vars {
			if v.Source == "" {
				return fmt.Errorf("mapping[%d].vars[%d]: source is required", i, j)
			}
			if v.Name == "" && v.Key != "" {
				return fmt.Errorf("mapping[%d].vars[%d]: name is required when key is set", i, j)
			}
			if _, ok := c.Sources[v.Source]; !ok {
				return fmt.Errorf("mapping[%d].vars[%d]: source %q is not defined", i, j, v.Source)
			}
		}
	}
	return nil
}
