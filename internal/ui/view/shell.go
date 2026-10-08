package view

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

func Render(content present.Shell, g Geometry, focus Pane, method ansi.Method) string {
	if g.Width <= 0 || g.Height <= 0 {
		return ""
	}
	if g.Small {
		lines := make([]string, g.Height)
		lines[0] = inline(uitext.Inline(uitext.TooSmall), g.Width, method)
		if g.Height > 1 {
			lines[g.Height-1] = Help(g.Width, focus, true, method)
		}
		return strings.Join(lines, "\n")
	}
	header := fit(uitext.Inline(uitext.App).String()+" | "+uitext.Inline(uitext.DefaultProfile).String(), g.Width, method)
	context := uitext.Inline(uitext.Organisation).String() + ": " + content.Organisation.String()
	if g.Columns == 1 {
		context = uitext.Inline(uitext.Organisation).String()
		if focus >= List {
			context += " > " + uitext.Inline(uitext.List).String()
		}
		if focus == Object {
			context += " > " + uitext.Inline(uitext.Object).String()
		}
	} else if g.Columns == 2 && focus == Navigation {
		context = uitext.Inline(uitext.Chooser).String()
	}
	lines := []string{header, fit(context, g.Width, method)}
	panes := Visible(g, focus)
	columns := make([][]string, len(panes))
	for i, p := range panes {
		width := g.ListWidth
		if g.Columns == 3 && p == Navigation {
			width = g.NavigationWidth
		}
		if g.Columns > 1 && p == Object {
			width = g.ObjectWidth
		}
		columns[i] = pane(content, p, width, g.BodyHeight, p == focus, method)
	}
	for row := 0; row < g.BodyHeight; row++ {
		parts := make([]string, len(columns))
		for i := range columns {
			parts[i] = columns[i][row]
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	return strings.Join(append(lines, Help(g.Width, focus, false, method)), "\n")
}

func pane(content present.Shell, which Pane, width, height int, focused bool, method ansi.Method) []string {
	title, text := uitext.Inline(uitext.Navigation), uitext.Body(uitext.NoData)
	switch which {
	case List:
		title, text = uitext.Inline(uitext.List), content.List
	case Object:
		title, text = uitext.Inline(uitext.Object), content.Object
	case Navigation:
		title = uitext.Inline(uitext.Chooser)
	}
	lines := make([]string, height)
	lines[0] = inline(title, width, method)
	if focused {
		lines[0] = lipgloss.NewStyle().Bold(true).Render(lines[0])
	}
	copy(lines[1:], body(text, width, height-1, method))
	for i := range lines {
		lines[i] += strings.Repeat(" ", max(0, width-method.StringWidth(lines[i])))
	}
	return lines
}
