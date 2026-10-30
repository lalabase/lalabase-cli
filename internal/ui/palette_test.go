package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The window takes the palette's ground and text on a terminal with colours,
// and stays the terminal's own under NO_COLOR, without a terminal and until
// the terminal's colours are known.
func TestTheWindowTakesThePaletteWhereColoursExist(t *testing.T) {
	if v := New().View(); v.BackgroundColor != nil || v.ForegroundColor != nil {
		t.Fatal("the window is painted before the colours are known")
	}
	for profile, want := range map[colorprofile.Profile]bool{
		colorprofile.TrueColor: true,
		colorprofile.ANSI256:   true,
		colorprofile.ANSI:      true,
		colorprofile.Ascii:     false,
		colorprofile.NoTTY:     false,
	} {
		next, _ := step(t, New(), tea.ColorProfileMsg{Profile: profile})
		v := next.View()
		if got := v.BackgroundColor == view.GroundColor && v.ForegroundColor == view.TextColor; got != want {
			t.Errorf("profile %v: painted %v, want %v", profile, got, want)
		}
		if !want && (v.BackgroundColor != nil || v.ForegroundColor != nil) {
			t.Errorf("profile %v: a colour is set", profile)
		}
	}
}
