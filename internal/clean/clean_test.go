package clean

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// goldenCases are the inputs and outputs of clean(value, inline) in bin/lala,
// the Python client this program replaces. The outputs were produced by running
// that function, so a case here fails when the port behaves differently.
type goldenCase struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Clean  string `json:"clean"`
	Inline string `json:"inline"`
}

func loadGolden(t testing.TB) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/lala_clean_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no golden cases")
	}
	return file.Cases
}

func TestTextMatchesThePythonClientByteForByte(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			if got := Text(c.Input); got != c.Clean {
				t.Errorf("Text(%q)\n got %q\nwant %q", c.Input, got, c.Clean)
			}
		})
	}
}

func TestInlineMatchesThePythonClientByteForByte(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			if got := Inline(c.Input); got != c.Inline {
				t.Errorf("Inline(%q)\n got %q\nwant %q", c.Input, got, c.Inline)
			}
		})
	}
}

// The assertions of test_clean_strips_escape_sequences_and_control_characters
// in spec/python/lala_test.py, kept as their own test so the intent survives a
// regenerated golden file.
func TestHostileTextLosesEveryDangerousCharacter(t *testing.T) {
	hostile := "\x1b]0;GEKAPERT\x07\x1b]52;c;ZWNobyBoaQ==\x07\x1b[2K\x1b[1A" +
		"\u009b2J" + "\u202eNEDRAWLAZ\u2066" + "<\u200b<< Ende" +
		"Zeile1\nfake-id\tFAKE ZEILE\u2028zweite\u2029dritte"
	cleaned := Text(hostile)
	for _, bad := range []string{"\x1b", "\x07", "\u009b", "\u202e", "\u2066", "\u200b", "\u2028", "\u2029"} {
		if strings.Contains(cleaned, bad) {
			t.Errorf("cleaned text still contains %q", bad)
		}
	}
	if !strings.Contains(cleaned, "Zeile1") {
		t.Error("ordinary text was lost")
	}
	// The split marker is whole again, so a later fence check sees it.
	if !strings.Contains(cleaned, "<<< Ende") {
		t.Error("a marker split by a zero-width space was not joined")
	}
}

// Go strings may carry bytes that are no UTF-8 at all; Python never sees
// those. A lone 0x9b byte is CSI on an 8-bit terminal.
func TestInvalidBytesBecomeTheReplacementCharacter(t *testing.T) {
	cases := map[string]string{
		"a\x9bb":     "a\ufffdb",
		"\xff\xfe":   "\ufffd\ufffd",
		"ok\xc2":     "ok\ufffd",
		"\xc2\x9b2J": " 2J", // a valid UTF-8 C1 character stays a control
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
		assertTerminalSafe(t, in, Text(in), false)
	}
}

// What reaches the terminal, checked on bytes: valid UTF-8, no C0 control
// except tab and newline, no DEL, no C1 control, no format character but the
// three that carry content. Inline text also has no tab, newline or CR.
func assertTerminalSafe(t testing.TB, in, out string, inline bool) {
	t.Helper()
	if !utf8.ValidString(out) {
		t.Fatalf("output of %q is not valid UTF-8: %q", in, out)
	}
	for _, b := range []byte(out) {
		if b == 0x1b || b == 0x07 || b == 0x7f {
			t.Fatalf("output of %q carries byte %#x: %q", in, b, out)
		}
		if b < 0x20 && b != '\t' && b != '\n' {
			t.Fatalf("output of %q carries control byte %#x: %q", in, b, out)
		}
	}
	for _, r := range out {
		switch {
		case r >= 0x80 && r <= 0x9f:
			t.Fatalf("output of %q carries C1 control %U: %q", in, r, out)
		case unicode.Is(unicode.Cf, r) && !keepFormat(r):
			t.Fatalf("output of %q carries format character %U: %q", in, r, out)
		case inline && (r == '\t' || r == '\n' || r == '\r'):
			t.Fatalf("inline output of %q carries %q: %q", in, r, out)
		}
	}
}

func TestEveryGoldenOutputIsTerminalSafe(t *testing.T) {
	for _, c := range loadGolden(t) {
		assertTerminalSafe(t, c.Input, Text(c.Input), false)
		assertTerminalSafe(t, c.Input, Inline(c.Input), true)
	}
}

func FuzzCleanedTextIsTerminalSafe(f *testing.F) {
	for _, c := range loadGolden(f) {
		f.Add(c.Input)
	}
	f.Add("a\x9bb\xff")
	f.Fuzz(func(t *testing.T, in string) {
		text, inline := Text(in), Inline(in)
		assertTerminalSafe(t, in, text, false)
		assertTerminalSafe(t, in, inline, true)
		// Cleaning twice changes nothing: nothing the first pass writes is
		// something the second pass would remove.
		if again := Text(text); again != text {
			t.Fatalf("Text is not idempotent on %q: %q then %q", in, text, again)
		}
		if again := Inline(inline); again != inline {
			t.Fatalf("Inline is not idempotent on %q: %q then %q", in, inline, again)
		}
	})
}
