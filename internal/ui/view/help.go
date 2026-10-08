package view

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

func Help(width int, focus Pane, small bool, method ansi.Method) string {
	if width <= 0 {
		return ""
	}
	ids := [][2]uitext.ID{{uitext.QuitKey, uitext.QuitHelp}, {uitext.InterruptKey, uitext.InterruptHelp}}
	if !small {
		ids = append(ids, [2]uitext.ID{uitext.FocusKey, uitext.FocusHelp}, [2]uitext.ID{uitext.EscapeKey, uitext.EscapeHelp})
		if focus == Navigation {
			ids = append(ids, [2]uitext.ID{uitext.EnterKey, uitext.EnterHelp})
		}
	}
	bindings := make([]key.Binding, 0, len(ids))
	used := 0
	for _, pair := range ids {
		label, description := uitext.Inline(pair[0]).String(), uitext.Inline(pair[1]).String()
		cells := method.StringWidth(label) + 1 + method.StringWidth(description)
		if len(bindings) > 0 {
			cells += 2
		}
		// Select complete entries before Bubbles styles them; help v2.2.1
		// can overflow when its ellipsis has no room.
		if used+cells > width {
			break
		}
		used += cells
		bindings = append(bindings, key.NewBinding(key.WithKeys(label), key.WithHelp(label, description)))
	}
	if len(bindings) == 0 {
		return inline(uitext.Inline(uitext.QuitKey), width, method)
	}
	h := help.New()
	h.SetWidth(width)
	h.ShortSeparator = "  "
	h.Ellipsis = uitext.Inline(uitext.Ellipsis).String()
	h.Styles = help.Styles{ShortKey: lipgloss.NewStyle().Bold(true)}
	return h.ShortHelpView(bindings)
}
