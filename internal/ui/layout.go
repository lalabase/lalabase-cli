package ui

import "github.com/gboyke/lalabase-cli/internal/ui/view"

// navigationWidth is the width of the navigation in three columns.
const navigationWidth = 24

// layout is the geometry with one help line and the list in focus, the
// shell's case.
func layout(width, height int) view.Geometry { return layoutWith(width, height, 1, view.List) }

// layoutWith is the geometry for a help of helpLines lines: the body gets
// what the header, the context line and the help leave. With the object in
// focus three columns become two: the list keeps its width and the object
// takes the navigation's, so a ticket or a notification is read wide.
func layoutWith(width, height, helpLines int, focus view.Pane) view.Geometry {
	width, height = max(0, width), max(0, height)
	helpLines = max(1, helpLines)
	g := view.Geometry{Width: width, Height: height, BodyHeight: max(0, height-2-helpLines), HelpHeight: helpLines, Small: width < 24 || height < 8}
	switch {
	case width >= 120:
		rest := width - navigationWidth - 2*view.SeparatorWidth
		g.Columns, g.NavigationWidth = 3, navigationWidth
		g.ListWidth = rest * 2 / 5
		g.ObjectWidth = rest - g.ListWidth
		if focus == view.Object {
			g.Columns, g.NavigationWidth = 2, 0
			g.ObjectWidth = width - view.SeparatorWidth - g.ListWidth
		}
	case width >= 80:
		g.Columns = 2
		g.ListWidth = (width - view.SeparatorWidth) * 2 / 5
		g.ObjectWidth = width - view.SeparatorWidth - g.ListWidth
	default:
		g.Columns, g.ListWidth = 1, width
	}
	return g
}

// geometry is the frame of the current state: the help's lines are counted
// first, so every active binding is shown; a terminal the help would not
// leave a body line in is small, whatever its size.
func (m Model) geometry() view.Geometry {
	g := layoutWith(m.width, m.height, len(view.HelpLines(m.help(), m.width, m.widthMethod)), m.focus)
	g.Small = g.Small || m.small()
	return g
}
