package view

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

func TestTextGeometryPreservesGraphemes(t *testing.T) {
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
