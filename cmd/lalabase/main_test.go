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
