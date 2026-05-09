// Package runner loads, executes, and reports on jitenv e2e scenarios.
//
// Design tenets, in order of importance:
//
//  1. The agent is the runner. The harness is a capture surface — it
//     does not branch behaviour on heuristics. Every verdict comes
//     from an explicit assert_* step in the YAML.
//  2. Run-dir layout is rigid. Acceptance criteria in scenarios refer
//     to paths in this layout (e.g. steps/<n>-<name>/exit), so the
//     layout is the contract; do not parameterise it.
//  3. Failures are diagnosable from the run dir alone. Every step
//     records cmd / stdout / stderr / exit. The teardown captures
//     agent.log, the config.toml state, and a docker-compose ps
//     snapshot.
package runner

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Scenario is the on-disk YAML shape. Keep this small — adding a new
// step type is easier than reasoning about a control-flow grammar.
type Scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Service is the docker-compose service to exec into for every step
	// that doesn't override it.
	Service string `yaml:"service"`
	// User defaults to "jitenv" — the non-root user every distro
	// container provisions. Override per-scenario if you need root.
	User  string `yaml:"user,omitempty"`
	Steps []Step `yaml:"steps"`
}

// Step is one entry in a scenario's `steps:` list. Exactly one of the
// action fields (Exec, AssertExitCode, …) is set per step; the loader
// validates that.
type Step struct {
	Name string `yaml:"name"`

	// Exec runs a shell command inside the service. Recorded as one
	// step in the run dir; subsequent assert_* steps refer to it via
	// `target` (default: most recent exec). The command is executed
	// with `bash -c` so users can pipe / redirect freely.
	Exec string `yaml:"exec,omitempty"`

	// User overrides the scenario default for this step (e.g. run a
	// helper as root before dropping back to the test user).
	User string `yaml:"user,omitempty"`

	// Service overrides the scenario default for this step.
	Service string `yaml:"service,omitempty"`

	// Env adds environment variables for this exec step. Inherited by
	// the container shell only; the harness never reads them.
	Env map[string]string `yaml:"env,omitempty"`

	// Stdin is fed to the command via docker exec -i.
	Stdin string `yaml:"stdin,omitempty"`

	// Timeout overrides the global per-step timeout for this step.
	Timeout string `yaml:"timeout,omitempty"`

	// Target picks an earlier step by name to assert against. When
	// empty, assert_* steps run against the most recent exec step.
	Target string `yaml:"target,omitempty"`

	// Assertion fields. A step has exactly one of these set when it
	// isn't an exec step.
	AssertExitCode         *int   `yaml:"assert_exit_code,omitempty"`
	AssertStdoutContains   string `yaml:"assert_stdout_contains,omitempty"`
	AssertStdoutEquals     string `yaml:"assert_stdout_equals,omitempty"`
	AssertStderrContains   string `yaml:"assert_stderr_contains,omitempty"`
	AssertStdoutNotContain string `yaml:"assert_stdout_not_contains,omitempty"`

	// WaitForFile polls inside the service until the named file exists
	// or the per-step timeout elapses. Useful for the agent socket.
	WaitForFile string `yaml:"wait_for_file,omitempty"`
}

// LoadScenario reads and validates a scenario file. It does not run it.
func LoadScenario(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%s: scenario name is required", path)
	}
	if s.Service == "" {
		return nil, fmt.Errorf("%s: scenario service is required", path)
	}
	if s.User == "" {
		s.User = "jitenv"
	}
	if len(s.Steps) == 0 {
		return nil, fmt.Errorf("%s: scenario has no steps", path)
	}
	for i, st := range s.Steps {
		if st.Name == "" {
			return nil, fmt.Errorf("%s: step[%d] is missing a name", path, i)
		}
		if err := validateStep(&st); err != nil {
			return nil, fmt.Errorf("%s: step[%d] (%s): %w", path, i, st.Name, err)
		}
	}
	return &s, nil
}

// validateStep enforces the "exactly one action" rule. Adding a new
// step type means adding a clause here and a handler in run.go.
func validateStep(st *Step) error {
	actions := 0
	if st.Exec != "" {
		actions++
	}
	if st.WaitForFile != "" {
		actions++
	}
	if st.AssertExitCode != nil {
		actions++
	}
	if st.AssertStdoutContains != "" {
		actions++
	}
	if st.AssertStdoutEquals != "" {
		actions++
	}
	if st.AssertStderrContains != "" {
		actions++
	}
	if st.AssertStdoutNotContain != "" {
		actions++
	}
	if actions == 0 {
		return fmt.Errorf("step has no action (set one of: exec, wait_for_file, assert_*)")
	}
	if actions > 1 {
		return fmt.Errorf("step has %d actions; only one per step is allowed", actions)
	}
	return nil
}
