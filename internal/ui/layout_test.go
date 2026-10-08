package ui

import (
	"reflect"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func TestLayoutBreakpoints(t *testing.T) {
	for _, tc := range []struct{ width, columns, nav, list, object int }{
		{79, 1, 0, 79, 0}, {80, 2, 0, 31, 48}, {119, 2, 0, 47, 71}, {120, 3, 24, 37, 57}, {160, 3, 24, 53, 81},
	} {
		g := layout(tc.width, 24)
		if g.Columns != tc.columns || g.NavigationWidth != tc.nav || g.ListWidth != tc.list || g.ObjectWidth != tc.object {
			t.Fatalf("width %d: %+v", tc.width, g)
		}
		if g.NavigationWidth+g.ListWidth+g.ObjectWidth+g.Columns-1 != tc.width {
			t.Fatalf("wrong cell budget: %+v", g)
		}
		if g.BodyHeight != 21 {
			t.Fatalf("body height %d", g.BodyHeight)
		}
	}
}

func TestCompactChooserReplacesList(t *testing.T) {
	for _, width := range []int{79, 80, 119, 120} {
		for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
			want := []view.Pane{view.Navigation, view.List, view.Object}
			if width < 80 {
				want = []view.Pane{focus}
			} else if width < 120 {
				left := view.List
				if focus == view.Navigation {
					left = view.Navigation
				}
				want = []view.Pane{left, view.Object}
			}
			if got := view.Visible(layout(width, 24), focus); !reflect.DeepEqual(got, want) {
				t.Fatalf("width %d focus %d: %v, want %v", width, focus, got, want)
			}
		}
	}
}

func TestLayoutSmallDimensions(t *testing.T) {
	for _, tc := range []struct {
		w, h  int
		small bool
	}{{24, 8, false}, {23, 8, true}, {24, 7, true}, {80, 24, false}, {0, 0, true}, {-1, -1, true}, {120, 1, true}} {
		g := layout(tc.w, tc.h)
		if g.Small != tc.small || g.Width < 0 || g.Height < 0 || g.BodyHeight < 0 {
			t.Fatalf("%+v: %+v", tc, g)
		}
		if !tc.small && g.BodyHeight != tc.h-3 {
			t.Fatalf("wrong body height: %+v", g)
		}
	}
}
