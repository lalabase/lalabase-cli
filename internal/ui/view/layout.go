package view

type Pane uint8

const (
	Navigation Pane = iota
	List
	Object
	// Agents is the Agents area (C9b): under the organisations in three
	// columns, in the list's place in two, alone in one.
	Agents
)

// Geometry is the frame of one render: the terminal, the columns and the
// lines the help takes at the bottom, so every active binding is shown and
// the body gets what is left. NoticeHeight is the number of lines the
// status notice takes under the path line (NoticeLines), 0 when it
// stands beside the path or, a hint, whole in its place.
type Geometry struct {
	Width, Height, BodyHeight               int
	Columns                                 int
	NavigationWidth, ListWidth, ObjectWidth int
	HelpHeight                              int
	NoticeHeight                            int
	Small                                   bool
}

func Visible(g Geometry, focus Pane) []Pane {
	if g.Columns == 3 {
		return []Pane{Navigation, List, Object}
	}
	if g.Columns == 1 {
		return []Pane{focus}
	}
	switch focus {
	case Navigation:
		return []Pane{Navigation, Object}
	case Agents:
		// The organisations stay above the agents.
		return []Pane{Navigation, Object}
	}
	return []Pane{List, Object}
}

// ColumnWidth is the width of one pane in this geometry, its frame
// included.
func ColumnWidth(g Geometry, which Pane) int {
	switch {
	case g.Columns == 3 && (which == Navigation || which == Agents):
		return g.NavigationWidth
	case g.Columns == 2 && which == Agents:
		return g.ListWidth
	case g.Columns > 1 && which == Object:
		return g.ObjectWidth
	}
	return g.ListWidth
}

// InnerWidth is the width of a pane's text: the pane less its frame's two
// sides and the space inside each.
func InnerWidth(g Geometry, which Pane) int {
	return max(0, ColumnWidth(g, which)-frameCells)
}
