package sourceguard

import (
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
	"testing"
)

// productionGraph is the import graph of the production files: directory →
// local directories it imports.
func productionGraph(t *testing.T, sources map[string]string) map[string][]string {
	t.Helper()
	graph := map[string][]string{}
	for name, source := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if local, ok := strings.CutPrefix(p, clientModule); ok {
				graph[path.Dir(name)] = append(graph[path.Dir(name)], local)
			}
		}
	}
	return graph
}

func reachableFrom(graph map[string][]string, root string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		for _, next := range graph[dir] {
			walk(next)
		}
	}
	walk(root)
	return seen
}

// The shipped binary carries neither the inbox fixture nor the contract
// copy: no production file the command reaches imports them. The fixture's
// tool is a program of its own, which is not released.
func TestTheFixtureAndTheContractStayOutOfTheBinary(t *testing.T) {
	sources := repositorySources(t)
	graph := productionGraph(t, sources)
	reachable := reachableFrom(graph, "cmd/lalabase")
	for _, barred := range []string{"internal/inboxfixture", "internal/contract", "tools/inbox-fixture"} {
		if reachable[barred] {
			t.Errorf("cmd/lalabase reaches %s", barred)
		}
	}
	if !reachable["internal/ui"] || !reachable["internal/gateway"] {
		t.Fatal("the graph misses what the command does reach")
	}
	// The contract copy is for tests and the fixture alone, wherever.
	for dir, imports := range graph {
		for _, imp := range imports {
			if imp == "internal/contract" {
				t.Errorf("%s imports the contract copy in production code", dir)
			}
		}
	}
	if _, ok := sources["internal/inboxfixture/fixture.go"]; !ok {
		t.Fatal("the fixture is not among the sources")
	}
	// The guard sees an import that would carry the fixture in.
	probe := map[string]string{
		"cmd/lalabase/main.go":              `package main; import _ "` + clientModule + `internal/ui"`,
		"internal/ui/model.go":              `package ui; import _ "` + clientModule + `internal/inboxfixture"`,
		"internal/inboxfixture/fixture.go":  `package inboxfixture`,
		"internal/gateway/gateway.go":       `package gateway`,
		"internal/ui/model_test.go":         `package ui; import _ "` + clientModule + `internal/contract"`,
		"internal/contract/contract.go":     `package contract`,
		"tools/inbox-fixture/main.go":       `package main; import _ "` + clientModule + `internal/inboxfixture"`,
		"tools/inbox-fixture/main_test.go":  `package main; import _ "` + clientModule + `internal/contract"`,
		"internal/sourceguard/fixture_test": `package sourceguard`,
	}
	g := productionGraph(t, probe)
	if r := reachableFrom(g, "cmd/lalabase"); !r["internal/inboxfixture"] {
		t.Fatal("the probe does not reach the fixture: the walk is broken")
	}
	for _, imports := range g {
		for _, imp := range imports {
			if imp == "internal/contract" {
				t.Fatal("a test import counted as production")
			}
		}
	}
}
