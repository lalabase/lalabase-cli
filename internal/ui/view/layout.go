package view

type Pane uint8

const (
	Navigation Pane = iota
	List
	Object
)

// Geometry is the frame of one render: the terminal, the columns and the
// lines the help takes at the bottom, so every active binding is shown and
// the body gets what is left.
type Geometry struct {
	Width, Height, BodyHeight               int
	Columns                                 int
	NavigationWidth, ListWidth, ObjectWidth int
	HelpHeight                              int
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

// ColumnWidth is the width of one pane in this geometry.
func ColumnWidth(g Geometry, which Pane) int {
	switch {
	case g.Columns == 3 && which == Navigation:
		return g.NavigationWidth
	case g.Columns > 1 && which == Object:
		return g.ObjectWidth
	}
	return g.ListWidth
}
