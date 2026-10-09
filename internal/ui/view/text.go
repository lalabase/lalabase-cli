package view

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

func inline(value present.Inline, width int, method ansi.Method) string {
	return fit(value.String(), width, method)
}

// Fit before styling. Complete graphemes are kept even in the terminal's
// wcwidth mode, where a joined emoji can consume several cells.
func fit(text string, width int, method ansi.Method) string {
	if width <= 0 {
		return ""
	}
	if method.StringWidth(text) <= width {
		return text
	}
	tail := uitext.Inline(uitext.Ellipsis).String()
	budget := width - method.StringWidth(tail)
	if budget < 0 {
		return ""
	}
	var out strings.Builder
	for len(text) > 0 {
		cluster, cells := ansi.FirstGraphemeCluster(text, method)
		if cells > budget {
			break
		}
		out.WriteString(cluster)
		budget -= cells
		text = text[len(cluster):]
	}
	out.WriteString(tail)
	return out.String()
}

// body wraps free text into at most height lines of width cells; an
// overflow is marked on the last line.
func body(value present.Body, width, height int, method ansi.Method) []string {
	return scrolled(present.Column{Text: value}, width, height, method)
}
