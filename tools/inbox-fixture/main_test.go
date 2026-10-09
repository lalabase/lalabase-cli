package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
)

func contractPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp_contract.json")
	if err := os.WriteFile(path, contract.Raw(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheToolServesTheFixtureOnTheLoopbackAddress(t *testing.T) {
	var out, errOut bytes.Buffer
	var served net.Addr
	code := run([]string{"-scenario", "older", "-port", "0", "-contract", contractPath(t)}, &out, &errOut, func(h http.Handler, ln net.Listener) error {
		served = ln.Addr()
		return nil
	})
	if code != 0 || served == nil {
		t.Fatalf("exit %d, %s", code, errOut.String())
	}
	addr := served.(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Fatalf("served on %v", addr)
	}
	if !strings.HasPrefix(out.String(), "inbox fixture: scenario older at http://127.0.0.1:") {
		t.Fatalf("output %q", out.String())
	}
}

func TestTheToolFailsClearly(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		code int
		want string
	}{
		"no contract":      {[]string{"-contract", filepath.Join(t.TempDir(), "missing.json")}, 1, "cannot read the contract copy"},
		"unknown scenario": {[]string{"-scenario", "bogus", "-contract", contractPath(t)}, 2, "unknown scenario"},
		"bad port":         {[]string{"-port", "70000", "-contract", contractPath(t)}, 1, "not a port"},
		"bad flag":         {[]string{"-nonsense"}, 2, "flag provided but not defined"},
	} {
		var out, errOut bytes.Buffer
		code := run(tc.args, &out, &errOut, func(http.Handler, net.Listener) error { t.Fatal("served"); return nil })
		if code != tc.code || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%s: exit %d, stderr %q", name, code, errOut.String())
		}
	}
}
