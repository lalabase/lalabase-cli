//go:build linux || darwin

package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func TestPTYReadinessRejectsIncompleteModes(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	if _, err := term.MakeRaw(int(slave.Fd())); err != nil {
		t.Fatal(err)
	}
	ready := "No data loaded\x1b[?1049h\x1b[?2004h\x1b[?25l"
	for _, missing := range []string{"", "No data loaded", "\x1b[?1049h", "\x1b[?2004h", "\x1b[?25l"} {
		c := &terminalProcess{slave: slave, deadline: time.Now().Add(60 * time.Millisecond), marker: "No data loaded"}
		if missing == "" {
			c.deadline = time.Now().Add(readyLimit)
		}
		text := ready
		if missing != "" {
			text = strings.ReplaceAll(text, missing, "")
		}
		c.output.WriteString(text)
		err := c.ready()
		if (err != nil) != (missing != "") {
			t.Fatalf("readiness with missing %q: %v", missing, err)
		}
		if err != nil && !strings.Contains(err.Error(), "readiness timeout") {
			t.Fatal(err)
		}
	}
}

func termiosRestorationError(before, after *unix.Termios) error {
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("termios not restored: before %+v, after %+v", before, after)
	}
	return nil
}

func terminalRestorationError(output string) error {
	for _, tc := range []struct {
		mode int
		end  bool
	}{{1049, false}, {25, true}, {2004, false}, {1002, false}, {1006, false}} {
		events := terminalModes(output)[tc.mode]
		if len(events) < 2 || events[0] == tc.end || events[len(events)-1] != tc.end {
			return fmt.Errorf("mode %d not restored: %v", tc.mode, events)
		}
	}
	return terminalOutputError(output)
}

func TestTermiosOracleComparesEveryAttribute(t *testing.T) {
	before := &unix.Termios{}
	if err := termiosRestorationError(before, before); err != nil {
		t.Fatal(err)
	}
	for i := range reflect.TypeFor[unix.Termios]().NumField() {
		after := *before
		field := reflect.ValueOf(&after).Elem().Field(i)
		if field.Kind() == reflect.Array {
			field = field.Index(0)
		}
		field.SetUint(field.Uint() ^ 1)
		if err := termiosRestorationError(before, &after); err == nil {
			t.Errorf("missed changed termios field %s", reflect.TypeFor[unix.Termios]().Field(i).Name)
		}
	}
}

func TestTerminalRestorationOracleRejectsCorruptedOutput(t *testing.T) {
	valid := "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?1002h\x1b[?1006hNo data loaded\x1b[?1049l\x1b[?25h\x1b[?2004l\x1b[?1002l\x1b[?1003l\x1b[?1006l"
	if err := terminalRestorationError(valid); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []int{1049, 25, 2004, 1002, 1006} {
		for _, final := range []byte{'h', 'l'} {
			sequence := fmt.Sprintf("\x1b[?%d%c", mode, final)
			bad := strings.ReplaceAll(valid, sequence, "")
			if err := terminalRestorationError(bad); err == nil {
				t.Errorf("accepted missing mode sequence %q", sequence)
			}
		}
		final := 'h'
		if mode == 25 {
			final = 'l'
		}
		if err := terminalRestorationError(valid + fmt.Sprintf("\x1b[?%d%c", mode, final)); err == nil {
			t.Errorf("accepted unrestored mode %d", mode)
		}
	}
	for _, bad := range []string{terminalHostile, "\x1b]52;c;YQ==\x07", "\x1b[31m", "\xff"} {
		if err := terminalRestorationError(valid + bad); err == nil {
			t.Errorf("restoration accepted hostile bytes %q", bad)
		}
	}
}
