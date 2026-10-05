package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut.String())
		}
		if !strings.HasPrefix(out.String(), "lalabase ") {
			t.Fatalf("%v: got %q", args, out.String())
		}
	}
}

func TestUnknownCommandFailsWithUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "Usage") {
		t.Fatalf("no usage on stderr: %q", errOut.String())
	}
}

func TestNoArgumentsShowsUsageForNow(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestHelpSucceedsAndPrintsUsage(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		if !strings.Contains(out.String(), "Usage") {
			t.Fatalf("%v: no usage on stdout: %q", args, out.String())
		}
	}
}

// The release stamps the tag with -ldflags "-X main.version=..."; that value
// wins over everything the build info says.
func TestAStampedVersionWins(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v9.9.9"

	var out, errOut bytes.Buffer
	run([]string{"version"}, &out, &errOut)
	if out.String() != "lalabase v9.9.9\n" {
		t.Fatalf("got %q", out.String())
	}
}
