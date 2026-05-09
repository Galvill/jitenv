// Command runner executes jitenv e2e scenarios. Each scenario is a
// YAML file that picks a docker-compose service to run inside, then
// drives a sequence of steps via `docker compose exec`. Output is
// captured per step into a run directory and a summary.json is
// produced at the end so a calling agent can inspect results without
// re-running anything.
//
// The harness DOES NOT bring the stack up or down — that's `make e2e-up`
// / `make e2e-down`. It DOES NOT interpret success based on heuristics
// — explicit assert_* steps are the only verdict surface.
//
// Usage:
//
//	runner -scenario e2e/scenarios/foo.yaml -compose-file e2e/docker-compose.yml -runs-dir e2e/runs
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gv/jitenv/e2e/harness/internal/runner"
)

func main() {
	var (
		scenarioPath = flag.String("scenario", "", "path to scenario YAML (required)")
		composeFile  = flag.String("compose-file", "e2e/docker-compose.yml", "path to docker-compose.yml")
		runsDir      = flag.String("runs-dir", "e2e/runs", "where to drop run artifacts")
		project      = flag.String("project", "jitenv-e2e", "compose project name")
	)
	flag.Parse()

	if *scenarioPath == "" {
		fmt.Fprintln(os.Stderr, "runner: -scenario is required")
		os.Exit(2)
	}

	abs, err := filepath.Abs(*scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner: %v\n", err)
		os.Exit(2)
	}
	scn, err := runner.LoadScenario(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner: load scenario: %v\n", err)
		os.Exit(2)
	}

	runDir := filepath.Join(*runsDir, fmt.Sprintf("%s-%s", scn.Name, time.Now().UTC().Format("20060102-150405")))
	if err := os.MkdirAll(runDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "runner: mkdir run-dir: %v\n", err)
		os.Exit(2)
	}

	r := &runner.Runner{
		Scenario:     scn,
		ScenarioPath: abs,
		RunDir:       runDir,
		ComposeFile:  *composeFile,
		Project:      *project,
		Out:          os.Stdout,
	}
	rep := r.Run()
	if err := rep.Write(runDir); err != nil {
		fmt.Fprintf(os.Stderr, "runner: write report: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "\nrun dir: %s\n", runDir)
	if rep.Verdict != "pass" {
		os.Exit(1)
	}
}
