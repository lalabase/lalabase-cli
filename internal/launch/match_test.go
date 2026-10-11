package launch

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/claudecfg"
)

func entry(scope, name, base string) claudecfg.Entry {
	e := claudecfg.Entry{Scope: scope, Name: name, BaseURL: base, Where: scope + "-file"}
	if base == "" {
		e.Problem = "runs a local program; nothing to import"
	}
	return e
}

func TestSameServer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"https://lalabase.com", "https://lalabase.com", true},
		{"https://lalabase.com/", "https://lalabase.com", true},
		{"https://LalaBase.COM", "https://lalabase.com", true},
		{"HTTPS://lalabase.com", "https://lalabase.com", true},
		{"https://lalabase.com:443", "https://lalabase.com", true},
		{"http://localhost:80", "http://localhost", true},
		{"https://lalabase.com/sub/", "https://lalabase.com/sub", true},
		{"https://lalabase.com/sub", "https://lalabase.com", false},
		{"https://lalabase.com/Sub", "https://lalabase.com/sub", false},
		{"http://lalabase.com", "https://lalabase.com", false},
		{"https://lalabase.com:8443", "https://lalabase.com", false},
		{"https://staging.lalabase.com", "https://lalabase.com", false},
		{"https://lalabase.com.evil.example", "https://lalabase.com", false},
		{"https://user@lalabase.com", "https://lalabase.com", false},
		{"https://lalabase.com?x=1", "https://lalabase.com", false},
		{"https://lalabase.com#x", "https://lalabase.com", false},
		{"ftp://lalabase.com", "ftp://lalabase.com", false},
		{"", "", false},
		{"lalabase.com", "lalabase.com", false},
	} {
		if got := sameServer(c.a, c.b); got != c.same {
			t.Errorf("sameServer(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

func TestPickServer(t *testing.T) {
	const profile = "https://lalabase.com"
	for _, c := range []struct {
		name    string
		entries []claudecfg.Entry
		want    string
		kind    Kind
		detail  string
	}{
		{name: "one", entries: []claudecfg.Entry{entry("user", "lalabase", profile)}, want: "lalabase"},
		{name: "none", entries: []claudecfg.Entry{entry("user", "lalabase", "https://staging.example")}, kind: NoServer, detail: "https://lalabase.com; Claude Code has: https://staging.example"},
		{name: "nothing at all", entries: nil, kind: NoServer, detail: "https://lalabase.com; Claude Code has: none"},
		{name: "other servers are not named as Lalabase servers", entries: []claudecfg.Entry{entry("user", "github", "")}, kind: NoServer, detail: "https://lalabase.com; Claude Code has: none"},
		// Two names with the same base: Claude Code's precedence, local
		// before project before user.
		{name: "two names, local first", entries: []claudecfg.Entry{entry("user", "a-user", profile), entry("local", "z-local", profile)}, want: "z-local"},
		{name: "two names, project before user", entries: []claudecfg.Entry{entry("user", "a-user", profile), entry("project", "m-project", profile)}, want: "m-project"},
		{name: "two names in one scope, by name", entries: []claudecfg.Entry{entry("user", "zeta", profile), entry("user", "alpha", profile)}, want: "alpha"},
		// One name in two scopes, both at the profile's server: whichever
		// Claude Code takes, it reads the right server.
		{name: "one name twice, same server", entries: []claudecfg.Entry{entry("user", "lalabase", profile), entry("local", "lalabase", profile+"/")}, want: "lalabase"},
		// A name that also points elsewhere is never used: Claude Code's
		// choice between the two is not ours to predict.
		{name: "shadowed by a project file", entries: []claudecfg.Entry{entry("user", "lalabase", profile), entry("project", "lalabase", "https://evil.example")}, kind: Conflict, detail: "lalabase; https://evil.example"},
		{name: "shadowed by a local program", entries: []claudecfg.Entry{entry("user", "lalabase", profile), entry("local", "lalabase", "")}, kind: Conflict, detail: "lalabase; local local-file: runs a local program; nothing to import"},
		{name: "shadowed name, another fits", entries: []claudecfg.Entry{entry("user", "lalabase", profile), entry("project", "lalabase", "https://evil.example"), entry("user", "prod", profile)}, want: "prod"},
		// The name goes into the prompt: outside the pattern it stops,
		// unless another name fits.
		{name: "bad name", entries: []claudecfg.Entry{entry("user", "lala base", profile)}, kind: BadName, detail: "lala base"},
		{name: "bad name with a quote", entries: []claudecfg.Entry{entry("project", "x'; rm -rf ~", profile)}, kind: BadName, detail: "x'; rm -rf ~"},
		{name: "name too long", entries: []claudecfg.Entry{entry("user", "a234567890123456789012345678901234567890123456789012345678901234x", profile)}, kind: BadName, detail: "a234567890123456789012345678901234567890123456789012345678901234x"},
		{name: "bad name, another fits", entries: []claudecfg.Entry{entry("local", "bad name", profile), entry("user", "good", profile)}, want: "good"},
		{name: "longest allowed name", entries: []claudecfg.Entry{entry("user", "a23456789012345678901234567890123456789012345678901234567890123_", profile)}, want: "a23456789012345678901234567890123456789012345678901234567890123_"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, kind, detail := pickServer(c.entries, profile)
			if got != c.want || kind != c.kind || detail != c.detail {
				t.Errorf("got %q, kind %v, detail %q; want %q, kind %v, detail %q", got, kind, detail, c.want, c.kind, c.detail)
			}
		})
	}
}
