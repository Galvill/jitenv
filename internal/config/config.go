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
	// VersionCheck gates the daily background check against
	// api.github.com/repos/Galvill/jitenv/releases/latest fired by
	// the shell hook (#136). Same *bool / default-on pattern as
	// PreRunNotice. Read access through VersionCheckEnabled.
	VersionCheck *bool `toml:"version_check,omitempty"`
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

// VersionCheckEnabled reports whether the shell hook should run the
// daily background check for a newer jitenv release. On by default;
// users who don't want an outbound HTTP call from a secrets tool
// can flip it off in config.toml (or via JITENV_NO_VERSION_CHECK=1
// for a per-shell opt-out).
func (a AgentConfig) VersionCheckEnabled() bool {
	if a.VersionCheck == nil {
		return true
	}
	return *a.VersionCheck
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
	Source string            `toml:"source,omitempty"`
	Ref    string            `toml:"ref,omitempty"`
	Key    string            `toml:"key,omitempty"`
	Extra  map[string]string `toml:"extra,omitempty"`
	// Value is a literal env-var value that bypasses source lookup.
	// Used by `jitenv clone` (#179) to wire GIT_ASKPASS to a stable
	// per-user askpass shim path that doesn't live in any bag. When
	// Value is set, Source/Ref/Key/Extra must be empty (Validate
	// enforces this); when Value is empty, the VarRef resolves via
	// the source machinery as before.
	Value string `toml:"value,omitempty"`
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

// isValidCommandName reports whether s is safe to interpolate into a
// generated shell wrapper as a bare command-name token. Windows ships
// a .ps1 wrapper per command (chpwd reconcile); any character active
// in PowerShell, bash, or zsh would let a config-controlled command
// name break out into executable syntax. Restrict to a portable set
// that covers real-world binary basenames (npm, node, g++, foo.exe,
// my_tool-v2) and rejects everything else.
func isValidCommandName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
}

// Validate performs all structural checks not covered by TOML parsing.
// It assumes the config is decrypted: ValidatePost cross-references
// var.source against the defined sources, which only resolves once the
// envelopes sealed by EncryptInPlace (#235) have been opened. Callers
// that operate on the encrypted on-disk form (e.g. `jitenv config
// validate`, which must run without the master key) should call
// ValidateStructure instead.
func (c *Config) Validate() error {
	if err := c.ValidateStructure(); err != nil {
		return err
	}
	return c.ValidatePost()
}

// ValidateStructure checks shape only — every check here is safe to run
// on the encrypted form because it tests field presence (non-empty) and
// the never-encrypted path/glob/cwd_glob/commands fields, never the
// decrypted content of a sealed var field. After #235, var.name/source/
// ref/key/value are envelope strings when loaded-but-not-decrypted; an
// envelope is still non-empty, so the "exactly one of path/glob/cwd",
// "value exclusive with source/ref/key/extra", "name required" rules
// all hold without the key.
func (c *Config) ValidateStructure() error {
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
				if !isValidCommandName(cmd) {
					return fmt.Errorf("mapping[%d].commands[%d]: command name %q contains characters outside [A-Za-z0-9._+-]; reject so the PowerShell wrapper can't be tricked into executing config-controlled syntax", i, j, cmd)
				}
			}
		}
		if len(m.Vars) == 0 {
			return fmt.Errorf("mapping[%d]: at least one var is required", i)
		}
		for j, v := range m.Vars {
			if v.Value != "" {
				// Literal-value VarRef. Source/Ref/Key/Extra must all be
				// empty: a value AND a source would mean two different
				// places to look for the same env var, which is just
				// confusing config. Name is required so we know what env
				// var to set.
				if v.Source != "" || v.Ref != "" || v.Key != "" || len(v.Extra) > 0 {
					return fmt.Errorf("mapping[%d].vars[%d]: value is exclusive with source/ref/key/extra", i, j)
				}
				if v.Name == "" {
					return fmt.Errorf("mapping[%d].vars[%d]: name is required for a literal-value var", i, j)
				}
				continue
			}
			if v.Source == "" {
				return fmt.Errorf("mapping[%d].vars[%d]: source is required (or set value for a literal)", i, j)
			}
			if v.Name == "" && v.Key != "" {
				return fmt.Errorf("mapping[%d].vars[%d]: name is required when key is set", i, j)
			}
		}
	}
	return nil
}

// ValidatePost performs the checks that require decrypted content —
// today just resolving each var.source to a defined source. It MUST run
// only after DecryptInPlace; on the encrypted form var.source is an
// envelope string that would never match a source-map key, so this
// would spuriously reject every valid config (#235).
func (c *Config) ValidatePost() error {
	for i, m := range c.Mappings {
		for j, v := range m.Vars {
			if v.Value != "" {
				continue
			}
			if _, ok := c.Sources[v.Source]; !ok {
				return fmt.Errorf("mapping[%d].vars[%d]: source %q is not defined", i, j, v.Source)
			}
		}
	}
	return nil
}
