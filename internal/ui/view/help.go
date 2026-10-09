package view

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// HelpLines renders the bindings, as many per line as fit, on as many
// lines as it takes: an active binding is never cut away. A binding wider
// than the line stands alone and is clipped there.
func HelpLines(bindings []present.KeyHelp, width int, method ansi.Method) []string {
	if width <= 0 || len(bindings) == 0 {
		return nil
	}
	var lines []string
	var line []key.Binding
	used := 0
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, helpLine(line, width))
		}
		line, used = nil, 0
	}
	for _, b := range bindings {
		label, description := b.Key.String(), b.Help.String()
		cells := method.StringWidth(label) + 1 + method.StringWidth(description)
		if len(line) > 0 && used+2+cells > width {
			flush()
		}
		if len(line) > 0 {
			used += 2
		}
		used += cells
		line = append(line, key.NewBinding(key.WithKeys(label), key.WithHelp(label, description)))
	}
	flush()
	return lines
}

// helpLine styles one line of bindings. The entries were selected to fit
// before Bubbles styles them; help v2.2.1 can overflow when its ellipsis
// has no room, and drops an entry wider than the line, so that one is
// clipped here instead.
func helpLine(bindings []key.Binding, width int) string {
	if len(bindings) == 1 {
		k := bindings[0].Help()
		if plain := k.Key + " " + k.Desc; ansi.StringWidth(plain) > width {
			return fit(plain, width, ansi.WcWidth)
		}
	}
	h := help.New()
	h.SetWidth(width)
	h.ShortSeparator = "  "
	h.Ellipsis = ""
	h.Styles = help.Styles{ShortKey: lipgloss.NewStyle().Bold(true)}
	return h.ShortHelpView(bindings)
}
