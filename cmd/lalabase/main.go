// Command lalabase is Lalabase from the terminal.
//
// This is slice C0 of the terminal client plan: the foundation (cleaning of
// foreign text, the contract, transport, profiles). The full-screen interface
// arrives in C1, the commands for agents in C4.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = ""

const usage = `Usage:
  lalabase version     print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "lalabase %s\n", buildVersion())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "lalabase: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// buildVersion prefers the version stamped by the release build, then the
// module version (go install ...@v0.1.0), then "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
