// Command lalabase is Lalabase from the terminal.
//
// The terminal shell is slice C1a. Data views arrive in C1b and C1c,
// and the commands for agents in C4.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"golang.org/x/term"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = ""

const usage = `Usage:
  lalabase [--profile default]
  lalabase [--profile NAME] <command>

Commands:
  auth import [--agent] [--profile NAME]
                       take a Lalabase server and token from Claude Code's
                       MCP configuration into a profile
  auth status          show the profiles, who their tokens belong to and what
                       the server supports
  version              print the version
`

// Env is what a command reads and writes: the process's own in main, a test's
// otherwise.
type Env struct {
	Context context.Context
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	Home    string
	Cwd     string
	// Interactive: stdin is a terminal, so a question can be asked.
	Interactive    bool
	OutputTerminal bool
	runUI          func(context.Context, io.Reader, io.Writer, []string) error
}

func main() {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	env := Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Home: home, Cwd: cwd, Interactive: isTerminal(os.Stdin), OutputTerminal: isTerminal(os.Stdout),
	}
	os.Exit(runWith(os.Args[1:], env))
}

// run is the entry point for callers that only need output (version, help).
func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, Env{Stdout: stdout, Stderr: stderr, Getenv: func(string) string { return "" }})
}

func runWith(args []string, env Env) int {
	// --version and --help look like flags but are commands.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			args = append([]string{"version"}, args[1:]...)
		case "--help", "-h":
			args = append([]string{"help"}, args[1:]...)
		}
	}
	global := flag.NewFlagSet("lalabase", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	profileName := global.String("profile", "", "profile to use")
	if err := global.Parse(args); err != nil {
		// Locale validation has not run. Quote even non-ASCII flag bytes.
		fmt.Fprintf(env.Stderr, "lalabase: %+q\n\n%s", err.Error(), usage)
		return 2
	}
	args = global.Args()
	if len(args) == 0 {
		return runInteractive(*profileName, env)
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(env.Stdout, "lalabase %s\n", buildVersion())
		return 0
	case "help":
		fmt.Fprint(env.Stdout, usage)
		return 0
	case "auth":
		return runAuth(args[1:], *profileName, env)
	default:
		fmt.Fprintf(env.Stderr, "lalabase: unknown command %+q\n\n%s", args[0], usage)
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

// isTerminal asks the terminal driver: a character device alone (/dev/null,
// /dev/zero) is no person to ask.
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
