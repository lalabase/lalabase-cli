package launch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/worker"
)

// C10: Enter on a file of the ticket opens it in the browser, on the
// core's file proxy, at an address built here from the profile's server
// and two checked ids. No string of a server's answer becomes an address.

const (
	fileOrg = "00000000-0000-4000-8000-000000000001"
	fileDoc = "00000000-0000-4000-8000-000000000002"
)

func TestFileAddressIsBuiltFromTheServerAndTheIDs(t *testing.T) {
	for _, tc := range []struct{ server, want string }{
		{"https://app.example.com", "https://app.example.com/org/" + fileOrg + "/files/ticket_document/" + fileDoc},
		{"https://app.example.com/", "https://app.example.com/org/" + fileOrg + "/files/ticket_document/" + fileDoc},
		{"https://app.example.com:8443/base/", "https://app.example.com:8443/base/org/" + fileOrg + "/files/ticket_document/" + fileDoc},
		{"http://localhost:3000", "http://localhost:3000/org/" + fileOrg + "/files/ticket_document/" + fileDoc},
	} {
		got, ok := fileAddress(tc.server, fileOrg, fileDoc)
		if !ok || got != tc.want {
			t.Fatalf("%s: %q, %v", tc.server, got, ok)
		}
	}
}

func TestFileAddressRefuses(t *testing.T) {
	bad := []string{"", "a", "..", "../..", "a/b", "a%2fb", "a\nb", "a b", "a?b", "a#b", fileDoc + "/", fileDoc + "\n", " " + fileDoc, "-" + fileDoc[1:] + "/x"}
	for _, id := range bad {
		if got, ok := fileAddress("https://app.example.com", fileOrg, id); ok {
			t.Fatalf("document %q gave %q", id, got)
		}
		if got, ok := fileAddress("https://app.example.com", id, fileDoc); ok {
			t.Fatalf("organisation %q gave %q", id, got)
		}
	}
	for _, server := range []string{"", "file:///etc", "javascript:alert(1)", "ftp://example.com", "x-app://open", "example.com", "HTTPS://", "https://", "http://"} {
		if got, ok := fileAddress(server, fileOrg, fileDoc); ok {
			t.Fatalf("server %q gave %q", server, got)
		}
	}
}

type openCall struct {
	path string
	args []string
}

// openLauncher is a launcher whose opener is a fake.
func openLauncher(t *testing.T, cfg Config, err error) (*Launcher, *[]openCall) {
	t.Helper()
	cfg.Supervisor = worker.New(t.Context())
	if cfg.Server == "" {
		cfg.Server = "https://app.example.com"
	}
	cfg.Token = "secret-token"
	l := New(cfg)
	calls := &[]openCall{}
	l.opener = func() (string, bool) { return "/bin/opener", true }
	l.runOpener = func(_ context.Context, path string, args []string) error {
		*calls = append(*calls, openCall{path, args})
		return err
	}
	return l, calls
}

func openDone(t *testing.T, l *Launcher, f File) Done {
	t.Helper()
	done := make(chan Done, 1)
	go func() {
		d, _ := l.OpenFile(7, Ticket{Org: fileOrg, ID: "tid", Number: 4}, f)().(Done)
		done <- d
	}()
	select {
	case d := <-done:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("no answer")
	}
	return Done{}
}

func TestOpenFileStartsTheOpenerWithOneArgument(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	d := openDone(t, l, File{ID: fileDoc, Name: "shot.png"})
	if d.Kind != FileOpened || d.Action != OpenFileAction || d.Ref != 7 || d.Detail != "shot.png" || d.Ticket.ID != "tid" {
		t.Fatalf("done = %+v", d)
	}
	want := "https://app.example.com/org/" + fileOrg + "/files/ticket_document/" + fileDoc
	if len(*calls) != 1 || (*calls)[0].path != "/bin/opener" || len((*calls)[0].args) != 1 || (*calls)[0].args[0] != want {
		t.Fatalf("calls = %+v", *calls)
	}
	if strings.Contains(strings.Join((*calls)[0].args, " "), "secret-token") {
		t.Fatal("the token is in the arguments")
	}
}

func TestOpenFileNameNeverReachesTheAddress(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	openDone(t, l, File{ID: fileDoc, Name: "evil.command"})
	if strings.Contains((*calls)[0].args[0], "evil") {
		t.Fatalf("args = %v", (*calls)[0].args)
	}
}

func TestOpenFileWithABadIDStartsNothing(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	if d := openDone(t, l, File{ID: "../x"}); d.Kind != NotHandedOver || d.Action != OpenFileAction {
		t.Fatalf("done = %+v", d)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestOpenFileOverSSHStartsNothing(t *testing.T) {
	l, calls := openLauncher(t, Config{Remote: true}, nil)
	if d := openDone(t, l, File{ID: fileDoc}); d.Kind != NoOpener {
		t.Fatalf("done = %+v", d)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestOpenFileWithoutAnOpener(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	l.opener = func() (string, bool) { return "", false }
	if d := openDone(t, l, File{ID: fileDoc}); d.Kind != NoOpener {
		t.Fatalf("done = %+v", d)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestOpenFileWhoseOpenerFails(t *testing.T) {
	l, _ := openLauncher(t, Config{}, errors.New("exit status 1"))
	if d := openDone(t, l, File{ID: fileDoc}); d.Kind != OpenFailed {
		t.Fatalf("done = %+v", d)
	}
}

// A program that does not end in time may still have opened the browser
// (xdg-open waits for it where no desktop runs): that is no failure.
func TestOpenFileHasADeadline(t *testing.T) {
	l, _ := openLauncher(t, Config{}, nil)
	l.deadline = time.Millisecond
	l.runOpener = func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if d := openDone(t, l, File{ID: fileDoc}); d.Kind != NoAnswer {
		t.Fatalf("done = %+v", d)
	}
}

func TestOpenFileEndedWithTheSession(t *testing.T) {
	l, _ := openLauncher(t, Config{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	l.cfg.Supervisor = worker.New(ctx)
	l.runOpener = func(ctx context.Context, _ string, _ []string) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	if d := l.openFile(1, Ticket{Org: fileOrg}, File{ID: fileDoc}); d.Kind != Canceled {
		t.Fatalf("done = %+v", d)
	}
}

func TestOpenerPathIsAbsolute(t *testing.T) {
	l := &Launcher{lookPath: func(string) (string, error) { return "relative/xdg-open", nil }}
	if path, ok := l.findOpener(); ok && !strings.HasPrefix(path, "/") {
		t.Fatalf("path = %q", path)
	}
}
