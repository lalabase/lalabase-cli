package view

type Pane uint8

const (
	Navigation Pane = iota
	List
	Object
)

type Geometry struct {
	Width, Height, BodyHeight               int
	Columns                                 int
	NavigationWidth, ListWidth, ObjectWidth int
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
