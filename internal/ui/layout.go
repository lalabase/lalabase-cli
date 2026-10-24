package ui

import (
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// navigationWidth is the width of the navigation in three columns.
const navigationWidth = 26

// layout is the geometry with one help line and the list in focus, the
// shell's case.
func layout(width, height int) view.Geometry { return layoutWith(width, height, 1, view.List) }

// layoutWith is the geometry for a help of helpLines lines: the body gets
// what the header, the context line and the help leave; the panes' widths
// are their frames' and add up to the width. With the object in
// focus three columns become two: the list keeps its width and the object
// takes the navigation's, so a ticket or a notification is read wide.
func layoutWith(width, height, helpLines int, focus view.Pane) view.Geometry {
	width, height = max(0, width), max(0, height)
	helpLines = max(1, helpLines)
	g := view.Geometry{Width: width, Height: height, BodyHeight: max(0, height-2-helpLines), HelpHeight: helpLines, Small: width < 24 || height < 8}
	switch {
	case width >= 120:
		rest := width - navigationWidth
		g.Columns, g.NavigationWidth = 3, navigationWidth
		g.ListWidth = rest * 2 / 5
		g.ObjectWidth = rest - g.ListWidth
		if focus == view.Object {
			g.Columns, g.NavigationWidth = 2, 0
			g.ObjectWidth = width - g.ListWidth
		}
	case width >= 80:
		g.Columns = 2
		g.ListWidth = width * 2 / 5
		g.ObjectWidth = width - g.ListWidth
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
	return m.withNotice(g)
}

// withNotice is the geometry with lines of their own for a status notice
// that does not fit beside the context line, taken from the body: as many
// as it wraps into, or as many as leave the focused column a row, the
// last of them cut. A notice never makes a terminal small: where not one
// line leaves that row, it stays beside the context, cut (a small
// terminal shows neither line).
func (m Model) withNotice(g view.Geometry) view.Geometry {
	if m.app == nil {
		return g
	}
	status, _ := m.status()
	if status.String() == "" {
		return g
	}
	s := present.Screen{Context: m.context(), Crumbs: m.crumbs(), Chooser: m.chooserColumn(), Status: status}
	focused := m.focusedColumn()
	for lines := view.NoticeLines(s, g, m.focus, m.widthMethod); lines > 0; lines-- {
		n := g
		n.NoticeHeight, n.BodyHeight = lines, g.BodyHeight-lines
		if view.Capacity(focused, n) >= 1 {
			return n
		}
	}
	return g
}
