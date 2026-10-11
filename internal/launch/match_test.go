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
		{"https://lalabase.com/t%65nant", "https://lalabase.com/t%65nant", false},
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

// Review round 1, F2: a host is compared only in ASCII. Go lowercases
// U+0130 to "i", the URL parser of Claude Code (WHATWG) maps it to
// punycode: another host.
func TestAHostOutsideASCIIIsNoServer(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"https://t\u0130ckets.example.com", "https://tickets.example.com"},
		{"https://tickets.example.com", "https://t\u0130ckets.example.com"},
		{"https://caf\u00e9.example", "https://caf\u00e9.example"},
		{"https://xn--caf-dma.example", "https://caf\u00e9.example"},
		{"https://lala%62ase.com", "https://lalabase.com"},
		{"https://lalabase.com\u212a", "https://lalabase.comk"},
	} {
		if sameServer(c.a, c.b) {
			t.Errorf("sameServer(%q, %q) is true", c.a, c.b)
		}
	}
	if !sameServer("https://xn--caf-dma.example", "https://XN--CAF-DMA.example/") {
		t.Error("punycode in ASCII is a host like any other")
	}
}

// Review round 1, F3: Claude Code names a server's tools after the name
// with every character outside [A-Za-z0-9_-] as "_", so two names that
// normalise alike are one name.
func TestToolNameOfAServer(t *testing.T) {
	for in, want := range map[string]string{
		"lalabase":           "lalabase",
		"lalabase.prod":      "lalabase_prod",
		"lalabase prod":      "lalabase_prod",
		"lala-base_1":        "lala-base_1",
		"caf\u00e9":          "caf_",
		"x\U0001F600":        "x__",
		"claude.ai Lalabase": "claude_ai_Lalabase",
		"claude.ai  x..y ":   "claude_ai_x_y",
	} {
		if got := toolName(in); got != want {
			t.Errorf("toolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickServerRoundOne(t *testing.T) {
	const profile = "https://lalabase.com"
	for _, c := range []struct {
		name    string
		entries []claudecfg.Entry
		want    string
		kind    Kind
		detail  string
	}{
		// F3: a server whose name normalises to the chosen one's.
		{name: "normalised alike", entries: []claudecfg.Entry{entry("user", "lalabase_prod", profile), entry("project", "lalabase.prod", "https://evil.example")}, kind: Conflict, detail: "lalabase_prod; https://evil.example"},
		{name: "normalised alike, both right", entries: []claudecfg.Entry{entry("user", "lalabase_prod", profile), entry("project", "lalabase prod", profile)}, want: "lalabase_prod"},
		// P5: a dot is outside the pattern.
		{name: "a dot in the name", entries: []claudecfg.Entry{entry("user", "lalabase.prod", profile)}, kind: BadName, detail: "lalabase.prod"},
		// P6: of two conflicts, the first name is reported.
		{name: "two conflicts", entries: []claudecfg.Entry{
			entry("user", "zeta", profile), entry("project", "zeta", "https://z.example"),
			entry("user", "alpha", profile), entry("project", "alpha", "https://a.example"),
		}, kind: Conflict, detail: "alpha; https://a.example"},
		// P7: a name's rank is the best scope it has.
		{name: "best scope of a name", entries: []claudecfg.Entry{entry("local", "beta", profile), entry("user", "beta", profile), entry("user", "alpha", profile)}, want: "beta"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, kind, detail := pickServer(c.entries, profile)
			if got != c.want || kind != c.kind || detail != c.detail {
				t.Errorf("got %q, kind %v, detail %q; want %q, kind %v, detail %q", got, kind, detail, c.want, c.kind, c.detail)
			}
		})
	}
}
