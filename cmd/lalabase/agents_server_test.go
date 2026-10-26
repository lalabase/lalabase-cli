package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/profile"
)

// testAgentsDir is the agents directory of the profile "default" on the
// server at url under home, as a terminal test's client sees it.
func testAgentsDir(home, url string) string {
	return agentsFiles(Env{Home: home, Getenv: func(string) string { return "" }}, profile.Profile{Name: "default", URL: url})
}

// Review round 4 of #34, F2: a profile's agents are kept per server. The
// same profile pointed at another server (a staging copy of a database,
// with the same ticket ids) reads none of the first server's agents; the
// same server written another way reads them all.
func TestAProfilesAgentsArePerServer(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	prod := profile.Profile{Name: "default", URL: "https://prod.example.com", Token: "prod-token"}
	dir := agentsFiles(env, prod)
	if dir == "" {
		t.Fatal("no agents directory")
	}
	if err := agents.Prepare(dir); err != nil {
		t.Fatal(err)
	}
	const agent = "0f8fad5b-d9cb-469f-a165-70867728950e"
	if err := agents.WriteLaunch(dir, agents.Launch{
		Agent: agent, Session: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Organisation: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Project: "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
		Ticket: "6ba7b812-9dad-11d1-80b4-00c04fd430c8", Number: 42, At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := agents.Hook(agents.HookCall{Dir: dir, Agent: agent, Event: agents.PermissionRequest, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		url  string
		want int
	}{
		{"https://staging.example.com", 0},
		{"http://prod.example.com", 0},
		{"https://prod.example.com/other", 0},
		{"https://prod.example.com", 1},
		{"https://PROD.example.com/", 1},
	} {
		other := agentsFiles(env, profile.Profile{Name: "default", URL: c.url, Token: "another-token"})
		if other == "" {
			t.Fatalf("%s: no agents directory", c.url)
		}
		got, err := agents.Read(other, nil)
		if err != nil && c.want > 0 {
			t.Fatalf("%s: %v", c.url, err)
		}
		if len(got) != c.want {
			t.Errorf("%s reads %d agents of %s, want %d", c.url, len(got), prod.URL, c.want)
		}
	}
	if got := agentsFiles(env, profile.Profile{Name: "default", URL: "not a server", Token: "t"}); got != "" {
		t.Fatalf("an address of no server has an agents directory: %s", got)
	}
}
