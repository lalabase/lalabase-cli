package view

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

func TestBodyWordWidthUsesCells(t *testing.T) {
	t.Parallel()

	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, tc := range []struct {
			width int
			want  []string
		}{
			{5, []string{"aa", "äää"}}, {6, []string{"aa äää"}}, {7, []string{"aa äää"}},
		} {
			if got := body(present.NewBody("aa äää"), tc.width, 3, method); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("method %d, width %d: %q, want %q", method, tc.width, got, tc.want)
			}
		}
	}
}

func TestBodyPreservesNonbreakingSpaces(t *testing.T) {
	t.Parallel()

	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, space := range []string{"\u00a0", "\u202f", "\u2007"} {
			text := "z." + space + "B. x"
			// An overlong word may break between graphemes, but its protected
			// space must stay intact instead of being discarded as a separator.
			want := []string{"z." + space + "B", ". x"}
			if got := body(present.NewBody(text), 4, 3, method); !reflect.DeepEqual(got, want) {
				t.Errorf("method %d, space %q: %q, want %q", method, space, got, want)
			}
			if got := body(present.NewBody("x "+"z."+space+"B."), 5, 3, method); !reflect.DeepEqual(got, []string{"x", "z." + space + "B."}) {
				t.Errorf("method %d: protected word was split: %q", method, got)
			}
		}
	}
}

// An entry that exactly fits stays on the line; one that does not goes to
// the next line, never away: every active binding is shown. A key is a key
// cap, a space either side of it, so it stays apart from its words
// without attributes.
func TestHelpWrapsInsteadOfDroppingBindings(t *testing.T) {
	t.Parallel()

	bindings := []present.KeyHelp{
		{Key: present.NewInline("q"), Help: present.NewInline("quit")},
		{Key: present.NewInline("ctrl+c"), Help: present.NewInline("interrupt")},
	}
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, tc := range []struct {
			width int
			want  []string
		}{
			{25, []string{" q quit", " ctrl+c interrupt"}}, {26, []string{" q quit   ctrl+c interrupt"}}, {27, []string{" q quit   ctrl+c interrupt"}},
			{5, []string{" q q…", " ctr…"}},
		} {
			lines := HelpLines(bindings, tc.width, method)
			for i := range lines {
				lines[i] = ansi.Strip(lines[i])
			}
			if !reflect.DeepEqual(lines, tc.want) {
				t.Errorf("method %d, width %d: %q, want %q", method, tc.width, lines, tc.want)
			}
		}
	}
	// The key cap is reversed, the description faint.
	if got := HelpLines(bindings, 80, ansi.WcWidth)[0]; !strings.Contains(got, "\x1b[7;38;2;216;218;223m q \x1b[m"+faint) || !strings.Contains(got, faint+"quit") {
		t.Fatalf("key caps: %q", got)
	}
	if HelpLines(nil, 80, ansi.WcWidth) != nil || HelpLines(bindings, 0, ansi.WcWidth) != nil {
		t.Fatal("help without bindings or width")
	}
}

func TestTextGeometryPreservesGraphemes(t *testing.T) {
	t.Parallel()

	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, tc := range []struct {
			s    string
			w    int
			want string
		}{{"abcdef", 3, "ab…"}, {"界a", 2, "…"}, {"界a", 3, "界a"}, {"e\u0301xy", 2, "e\u0301…"}, {"abc", 0, ""}, {"abc", -1, ""}} {
			got := inline(present.NewInline(tc.s), tc.w, method)
			if got != tc.want || !utf8.ValidString(got) || method.StringWidth(got) > max(0, tc.w) {
				t.Fatalf("method %d: %q at %d: %q, want %q", method, tc.s, tc.w, got, tc.want)
			}
		}
		for _, cluster := range []string{"👩\u200d👩\u200d👧\u200d👦", "🇩🇪", "👍🏽", "e\u0301"} {
			width := method.StringWidth(cluster) + 1
			if got := inline(present.NewInline(cluster+"XY"), width, method); got != cluster+"…" {
				t.Fatalf("split cluster, method %d: %q", method, got)
			}
		}
		if got := body(present.NewBody("one\ntwo\nthree"), 5, 2, method); !reflect.DeepEqual(got, []string{"one", "two…"}) {
			t.Fatalf("vertical clip: %q", got)
		}
		for _, width := range []int{0, 1, 2, 7} {
			got := body(present.NewBody("界\tlongword\ne\u0301 👩\u200d👩\u200d👧\u200d👦"), width, 3, method)
			if len(got) > 3 || (width == 0 && len(got) != 0) {
				t.Fatalf("body dimensions: %q", got)
			}
			for _, line := range got {
				if strings.Contains(line, "\t") || method.StringWidth(line) > width || !utf8.ValidString(line) {
					t.Fatalf("bad body line %q at %d", line, width)
				}
			}
		}
	}
}

func TestBodyDimensionBoundaries(t *testing.T) {
	t.Parallel()

	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, tc := range []struct {
			width, height int
			want          []string
		}{
			{5, -1, nil}, {5, 0, nil}, {-1, 3, nil}, {0, 3, nil},
			{5, 1, []string{"one…"}}, {5, 2, []string{"one", "two…"}},
			{5, 3, []string{"one", "two", "three"}}, {5, 4, []string{"one", "two", "three"}},
		} {
			if got := body(present.NewBody("one\ntwo\nthree"), tc.width, tc.height, method); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("method %d, %dx%d: got %q, want %q", method, tc.width, tc.height, got, tc.want)
			}
		}
	}
}

func TestBodyWrappingPreservesGraphemeClusters(t *testing.T) {
	t.Parallel()

	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, cluster := range []string{"e\u0301", "e\u0301\u0327", "1\ufe0f\u20e3", "🇩🇪", "👍🏽", "👩\u200d👩\u200d👧\u200d👦"} {
			prefix := strings.Repeat("a", 24-method.StringWidth(cluster))
			text := prefix + cluster + "x"
			for _, tc := range []struct {
				width int
				want  []string
			}{
				{23, []string{prefix, cluster + "x"}},
				{24, []string{prefix + cluster, "x"}},
				{25, []string{text}},
			} {
				got := body(present.NewBody(text), tc.width, 8, method)
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("method %d, cluster %q at width %d: got %q, want %q", method, cluster, tc.width, got, tc.want)
				}
				if strings.Join(got, "") != text {
					t.Errorf("method %d, width %d: wrapping lost text: %q", method, tc.width, got)
				}
			}
		}
		for _, tc := range []struct {
			text  string
			width int
			want  []string
		}{
			{"one two", 5, []string{"one", "two"}},
			{"one-two", 5, []string{"one-", "two"}},
			{"  a\n\nb\n", 5, []string{"  a", "", "b", ""}},
			{"a\tb", 8, []string{"a    b"}},
			{"e\u0301x", 1, []string{"e\u0301", "x"}},
			{"界x", 1, []string{"…", "x"}},
		} {
			if got := body(present.NewBody(tc.text), tc.width, 8, method); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("method %d, %q at width %d: got %q, want %q", method, tc.text, tc.width, got, tc.want)
			}
		}
	}
}
