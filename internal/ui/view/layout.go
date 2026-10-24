package view

type Pane uint8

const (
	Navigation Pane = iota
	List
	Object
)

// Geometry is the frame of one render: the terminal, the columns and the
// lines the help takes at the bottom, so every active binding is shown and
// the body gets what is left. NoticeHeight is the number of lines the
// status notice takes under the context line (NoticeLines), 0 when it
// stands beside it.
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
	if focus == Navigation {
		return []Pane{Navigation, Object}
	}
	return []Pane{List, Object}
}

// ColumnWidth is the width of one pane in this geometry, its frame
// included.
func ColumnWidth(g Geometry, which Pane) int {
	switch {
	case g.Columns == 3 && which == Navigation:
		return g.NavigationWidth
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
