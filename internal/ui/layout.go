package ui

import "github.com/gboyke/lalabase-cli/internal/ui/view"

func layout(width, height int) view.Geometry {
	width, height = max(0, width), max(0, height)
	g := view.Geometry{Width: width, Height: height, BodyHeight: max(0, height-3), Small: width < 24 || height < 8}
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
