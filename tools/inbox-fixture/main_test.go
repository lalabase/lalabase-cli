package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
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

// K1: the tool serves the ticket view over REST by default, its /me
// listing the contract's capabilities; -before-r1 serves a server before
// R1, on which the client takes the tools.
func TestTheToolServesBothWays(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		rest bool
		line string
	}{
		"rest":      {nil, true, "the ticket view over REST"},
		"before R1": {[]string{"-before-r1"}, false, "the ticket view over the tools (a server before R1)"},
	} {
		var out, errOut bytes.Buffer
		var handler http.Handler
		args := append([]string{"-port", "0", "-contract", contractPath(t)}, c.args...)
		code := run(args, &out, &errOut, func(h http.Handler, _ net.Listener) error {
			handler = h
			return nil
		})
		if code != 0 || handler == nil {
			t.Fatalf("%s: exit %d, %s", name, code, errOut.String())
		}
		if !strings.Contains(out.String(), c.line) {
			t.Errorf("%s: output %q", name, out.String())
		}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+inboxfixture.Token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got := strings.Contains(rec.Body.String(), `"tickets.paging"`); got != c.rest {
			t.Errorf("%s: /me lists tickets.paging %v: %s", name, got, rec.Body.String())
		}
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
