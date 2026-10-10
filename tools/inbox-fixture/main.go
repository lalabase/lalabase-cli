// Command inbox-fixture serves the loopback stand-in of a Lalabase server
// (internal/inboxfixture) for a person trying the interface by hand:
//
//	go run ./tools/inbox-fixture -scenario normal -port 18089
//
// then, in another terminal, lalabase with a profile whose url is
// http://127.0.0.1:18089 and whose token is the fixture's. It reads the
// contract copy from the repository (internal/contract stays a package only
// tests import), binds to 127.0.0.1 only and serves until it is killed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// maxContract bounds the contract copy read from disk.
const maxContract = 16 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, func(h http.Handler, ln net.Listener) error {
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
		return srv.Serve(ln)
	}))
}

// run parses the flags, builds the fixture and hands it to serve, which
// blocks (the program) or returns at once (a test).
func run(args []string, stdout, stderr io.Writer, serve func(http.Handler, net.Listener) error) int {
	fs := flag.NewFlagSet("inbox-fixture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scenario := fs.String("scenario", string(inboxfixture.Normal), "one of normal, older, temporary, 429, 401, vanished")
	port := fs.Int("port", 18089, "the port on 127.0.0.1")
	contractPath := fs.String("contract", "internal/contract/testdata/mcp_contract.json", "the contract copy")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, _, err := safefile.ReadRegular(*contractPath, maxContract)
	if err != nil {
		fmt.Fprintf(stderr, "inbox-fixture: cannot read the contract copy at %s: %v\n", *contractPath, err)
		return 1
	}
	f, err := inboxfixture.New(inboxfixture.Scenario(*scenario), data)
	if err != nil {
		fmt.Fprintf(stderr, "inbox-fixture: %v\n", err)
		return 2
	}
	ln, err := inboxfixture.Listen(*port)
	if err != nil {
		fmt.Fprintf(stderr, "inbox-fixture: %v\n", err)
		return 1
	}
	defer func() { _ = ln.Close() }()
	fmt.Fprintln(stdout, inboxfixture.Describe(inboxfixture.Scenario(*scenario), ln.Addr()))
	if err := serve(f, ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "inbox-fixture: %v\n", err)
		return 1
	}
	return 0
}
