package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/ui"
)

func TestTheFixtureRefusesOnceThenGoesThrough(t *testing.T) {
	s := spec(false)
	first := s.Send(ui.Submission{Dialog: 7, Values: []string{"30", "note"}})
	second := s.Send(ui.Submission{Dialog: 7, Values: []string{"45", ""}})
	if first == nil || second == nil {
		t.Fatal("a send without a command")
	}
	refused, ok := first().(ui.DialogOutcome)
	if !ok || refused.Dialog != 7 || refused.Done || !strings.Contains(refused.Refusal.String(), `"30" "note"`) {
		t.Fatalf("first outcome %+v", refused)
	}
	if done, ok := second().(ui.DialogOutcome); !ok || !done.Done {
		t.Fatalf("second outcome %+v", done)
	}
}

func TestTheLargeFixtureHasOneFieldOfSeveralLines(t *testing.T) {
	if s := spec(true); !s.Large || len(s.Fields) != 1 {
		t.Fatalf("spec %+v", s)
	}
	if s := spec(false); s.Large || len(s.Fields) != 2 {
		t.Fatalf("spec %+v", s)
	}
}

func TestAnUnknownFlagEndsWithUsage(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"-nope"}, strings.NewReader(""), &bytes.Buffer{}, &stderr, nil); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "-large") {
		t.Fatalf("usage %q", stderr.String())
	}
}
