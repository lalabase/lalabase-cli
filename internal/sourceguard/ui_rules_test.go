package sourceguard

import "testing"

func TestUIRejectsIndirectOutputAndWorkers(t *testing.T) {
	for _, source := range []string{
		`import lg "charm.land/lipgloss/v2"; var stream = lg.Writer`,
		`import "charm.land/lipgloss/v2"; func bad(){ lipgloss.Writer.Write(nil) }`,
		`import lg "charm.land/lipgloss/v2"; var output = lg.Fprint`,
		`import lg "charm.land/lipgloss/v2"; var output = lg.Fprintf`,
		`import lg "charm.land/lipgloss/v2"; var output = lg.Fprintln`,
		`import u "golang.org/x/sys/unix"; func bad(){ u.Write(1, nil) }`,
		`import term "golang.org/x/term"; var raw = term.MakeRaw`,
		`import "sync"; func bad(){ var wg sync.WaitGroup; wg.Go(func(){}) }`,
		`import clock "time"; var spawn = clock.AfterFunc`,
		`import ctx "context"; var spawn = ctx.AfterFunc`,
	} {
		for _, name := range []string{"bad.go", "bad_test.go", "bad_linux.go", "bad_darwin.go"} {
			for _, dir := range []string{"internal/ui", "internal/helper"} {
				files := map[string]string{
					"internal/ui/view/pane.go": "package view; import \"" + clientModule + "internal/helper\"",
					dir + "/" + name:           "package probe; " + source,
				}
				if got := inspectUI(files); len(got) == 0 {
					t.Errorf("missed %s/%s: %s", dir, name, source)
				}
			}
		}
	}
	if got := inspectUI(map[string]string{"internal/ui/good.go": `package ui; import "context"; import "time"; import "sync"; var _ = context.WithTimeout; var _ = time.NewTimer; var _ sync.WaitGroup`}); len(got) != 0 {
		t.Fatalf("safe context, timer or wait group rejected: %v", got)
	}
}

func TestUIEntryRequiresProtectedRoot(t *testing.T) {
	imports := `package main; import shell "github.com/gboyke/lalabase-cli/internal/ui"; import "github.com/gboyke/lalabase-cli/internal/inbox"; `
	for _, file := range []string{"main.go", "main_linux.go", "main_darwin.go"} {
		for _, code := range []string{
			`func run(){ shell.Run(ctx, in, out, env, inbox.New()) }`,
			`func run(){ model := inbox.New(); shell.Run(ctx, in, out, env, model) }`,
			`var start = shell.Run`,
		} {
			if got := inspectUI(map[string]string{"cmd/lalabase/" + file: imports + code}); len(got) == 0 {
				t.Errorf("unprotected UI entry accepted: %s", code)
			}
		}
	}
	for _, constructor := range []string{`shell.New()`, `shell.NewWithContent("a", "b", "c")`} {
		if got := inspectUI(map[string]string{"cmd/lalabase/main.go": imports + `func run(){ shell.Run(ctx, in, out, env, ` + constructor + `) }`}); len(got) != 0 {
			t.Errorf("protected root rejected: %v", got)
		}
	}
	// PTY tests intentionally decorate the root to inject faults. Runtime
	// callers must use a protected root constructor directly.
	if got := inspectUI(map[string]string{"cmd/lalabase/terminal_test.go": imports + `func test(){ shell.Run(ctx, in, out, env, faultModel{}) }`}); len(got) != 0 {
		t.Fatal(got)
	}
}
