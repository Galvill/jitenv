package main

import (
	"fmt"
	"os"

	"github.com/gv/jitenv/internal/cli"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
