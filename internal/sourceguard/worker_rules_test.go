package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const startSource = `package worker
type Supervisor struct{}
func (s *Supervisor) Start(where string, f func()) bool { go f(); return true }
`

func TestProductionGoroutinesOnlyInTheWorkersStart(t *testing.T) {
	if got := inspectUI(map[string]string{workerFile: startSource}); len(got) != 0 {
		t.Fatalf("the worker's Start was rejected: %v", got)
	}
	for name, source := range map[string]string{
		// The same file, but not the exact function.
		workerFile + "#other": startSource + `func (s *Supervisor) Spawn(f func()) { go f() }`,
		workerFile + "#plain": startSource + `func Start(f func()) { go f() }`,
		workerFile + "#type":  startSource + `type Other struct{}; func (o *Other) Start(f func()) { go f() }`,
		workerFile + "#value": startSource + `func (s Supervisor) Run(f func()) { go f() }`,
		workerFile + "#var":   startSource + `var spawn = func(f func()) { go f() }`,
		// The same package, another file.
		"internal/worker/extra.go": `package worker; type Supervisor struct{}; func (s *Supervisor) Start(f func()) { go f() }`,
		// Anywhere else, not only where the interface reaches.
		"internal/queue/queue.go":    `package queue; func run(f func()) { go f() }`,
		"internal/gateway/x.go":      `package gateway; import "sync"; func run(wg *sync.WaitGroup) { wg.Go(func(){}) }`,
		"internal/profile/lock.go":   `package profile; import "time"; var spawn = time.AfterFunc`,
		"cmd/lalabase/main.go":       `package main; import c "context"; var spawn = c.AfterFunc`,
		"tools/update-contract/x.go": `package main; func run(f func()) { go f() }`,
	} {
		file, _, _ := strings.Cut(name, "#")
		if got := inspectUI(map[string]string{file: source}); len(got) == 0 {
			t.Errorf("missed a production goroutine in %s", name)
		}
	}
}

func TestPanicRecoveryStaysOnEverywhere(t *testing.T) {
	for _, name := range []string{"cmd/lalabase/interactive.go", "cmd/lalabase/terminal_test.go", "internal/gateway/gateway.go"} {
		source := `package p; import t "charm.land/bubbletea/v2"; var option = t.WithoutCatchPanics`
		if got := inspectUI(map[string]string{name: source}); len(got) == 0 {
			t.Errorf("missed WithoutCatchPanics in %s", name)
		}
	}
}

func TestTheRootControllerMayUseTheInfrastructureItsViewsMayNot(t *testing.T) {
	root := `package ui; import (
	_ "` + clientModule + `internal/gateway"
	_ "` + clientModule + `internal/queue"
	_ "` + clientModule + `internal/cache"
	_ "` + clientModule + `internal/api"
	_ "` + clientModule + `internal/transport"
	_ "` + clientModule + `internal/worker"
)`
	deps := map[string]string{
		"internal/ui/model.go":         root,
		"internal/gateway/gateway.go":  `package gateway; import _ "` + clientModule + `internal/transport"`,
		"internal/transport/client.go": `package transport; import ( _ "net/http"; _ "os" )`,
	}
	if got := inspectUI(deps); len(got) != 0 {
		t.Fatalf("the root controller's infrastructure was rejected: %v", got)
	}
	for _, imp := range []string{"os", "net/http", "syscall"} {
		if got := inspectUI(map[string]string{"internal/ui/model.go": `package ui; import _ "` + imp + `"`}); len(got) == 0 {
			t.Errorf("the root controller imported %s directly", imp)
		}
	}
	// A view reaches the gateway directly, through a helper, or through the
	// root controller.
	for name, source := range map[string]string{
		"direct": `package view; import _ "` + clientModule + `internal/gateway"`,
		"helper": `package view; import _ "` + clientModule + `internal/helper"`,
		"root":   `package view; import _ "` + clientModule + `internal/ui"`,
	} {
		files := map[string]string{
			"internal/ui/view/pane.go":    source,
			"internal/helper/helper.go":   `package helper; import _ "` + clientModule + `internal/gateway"`,
			"internal/ui/model.go":        root,
			"internal/gateway/gateway.go": `package gateway`,
		}
		if got := strings.Join(inspectUI(files), " "); !strings.Contains(got, "internal/ui/view/pane.go") {
			t.Errorf("a view reached the gateway %s unnoticed: %s", name, got)
		}
	}
}

func TestWorkerPathsCannotWriteToTheTerminal(t *testing.T) {
	for _, dir := range []string{"internal/gateway", "internal/worker", "internal/queue"} {
		for _, file := range []string{"x.go", "x_test.go"} {
			source := `package p; import f "fmt"; var output = f.Println`
			if got := inspectUI(map[string]string{dir + "/" + file: source}); len(got) == 0 {
				t.Errorf("missed output in %s/%s", dir, file)
			}
		}
	}
	// What the worker paths reach is checked too.
	files := map[string]string{
		"internal/gateway/gateway.go": `package gateway; import _ "` + clientModule + `internal/api"`,
		"internal/api/api.go":         `package api; import sys "os"; var output = sys.Stdout`,
	}
	if got := inspectUI(files); len(got) == 0 {
		t.Error("missed output in a package the gateway reaches")
	}
}

// The real files of the new packages are read: a goroutine or an output
// added to them turns the guard red.
func TestTheGuardReadsTheRealWorkerFiles(t *testing.T) {
	root := repoRoot(t)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, name := range []string{"internal/gateway/gateway.go", "internal/gateway/scope.go", "internal/queue/queue.go", workerFile} {
		real := read(name)
		if got := inspectUI(map[string]string{name: real}); len(got) != 0 {
			t.Fatalf("%s: %v", name, got)
		}
		for _, probe := range []string{"\nfunc probe(f func()) { go f() }\n", "\nfunc probe() { print(1) }\n"} {
			if got := inspectUI(map[string]string{name: real + probe}); len(got) == 0 {
				t.Errorf("%s with %q passed", name, probe)
			}
		}
	}
}

// Review #9, finding 7: the gaps a reviewer found.
func TestReviewGapsInTheGoroutineAndOutputRules(t *testing.T) {
	for name, source := range map[string]string{
		"cmd/lalabase/main.go#dot time":    `package main; import . "time"; var spawn = AfterFunc`,
		"cmd/lalabase/main.go#dot context": `package main; import . "context"; var spawn = AfterFunc`,
		"internal/queue/queue.go#trygo":    `package queue; import "golang.org/x/sync/errgroup"; func run(g *errgroup.Group) { g.TryGo(func() error { return nil }) }`,
		"internal/queue/queue.go#dochan":   `package queue; import "golang.org/x/sync/singleflight"; func run(g *singleflight.Group) { g.DoChan("k", nil) }`,
		"internal/gateway/x.go#log":        `package gateway; import "log"; func run() { log.Print("x") }`,
		"internal/gateway/x.go#slog":       `package gateway; import "log/slog"; func run() { slog.Info("x") }`,
		"internal/worker/x_test.go#log":    `package worker; import l "log"; var output = l.Println`,
	} {
		file, _, _ := strings.Cut(name, "#")
		if got := inspectUI(map[string]string{file: source}); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
	// A package the worker paths do not reach may still log.
	if got := inspectUI(map[string]string{"tools/update-contract/main.go": `package main; import "log"; func run() { log.Print("x") }`}); len(got) != 0 {
		t.Fatalf("unrelated logging rejected: %v", got)
	}
}
