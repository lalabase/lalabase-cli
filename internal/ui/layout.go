package ui

import "github.com/gboyke/lalabase-cli/internal/ui/view"

// layout is the geometry with one help line, the shell's case.
func layout(width, height int) view.Geometry { return layoutWith(width, height, 1) }

// layoutWith is the geometry for a help of helpLines lines: the body gets
// what the header, the context line and the help leave.
func layoutWith(width, height, helpLines int) view.Geometry {
	width, height = max(0, width), max(0, height)
	helpLines = max(1, helpLines)
	g := view.Geometry{Width: width, Height: height, BodyHeight: max(0, height-2-helpLines), HelpHeight: helpLines, Small: width < 24 || height < 8}
	switch {
	case width >= 120:
		g.Columns, g.NavigationWidth = 3, 24
		g.ListWidth = (width - 26) * 2 / 5
		g.ObjectWidth = width - 26 - g.ListWidth
	case width >= 80:
		g.Columns = 2
		g.ListWidth = (width - 1) * 2 / 5
		g.ObjectWidth = width - 1 - g.ListWidth
	default:
		g.Columns, g.ListWidth = 1, width
	}
	return g
}

// geometry is the frame of the current state: the help's lines are counted
// first, so every active binding is shown.
func (m Model) geometry() view.Geometry {
	return layoutWith(m.width, m.height, len(view.HelpLines(m.help(), m.width, m.widthMethod)))
}
