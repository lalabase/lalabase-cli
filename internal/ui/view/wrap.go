package view

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapBody wraps cleaned, unstyled text at spaces and hyphens. Long words
// break only between complete graphemes, including those with ASCII bases.
// An oversized grapheme occupies its own line for fit to clip as a unit.
func wrapBody(text string, width int, method ansi.Method) []string {
	var lines []string
	var line, word, space strings.Builder
	var lineWidth, wordWidth, spaceWidth int
	newline := func() {
		lines = append(lines, line.String())
		line.Reset()
		lineWidth = 0
	}
	flushSpace := func() {
		if lineWidth+spaceWidth <= width {
			line.WriteString(space.String())
			lineWidth += spaceWidth
		}
		space.Reset()
		spaceWidth = 0
	}
	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		if lineWidth+spaceWidth+wordWidth > width {
			if line.Len() > 0 {
				newline()
			}
			space.Reset()
			spaceWidth = 0
		}
		flushSpace()
		for rest := word.String(); rest != ""; {
			cluster, cells := ansi.FirstGraphemeCluster(rest, method)
			if line.Len() > 0 && lineWidth+cells > width {
				newline()
			}
			line.WriteString(cluster)
			lineWidth += cells
			rest = rest[len(cluster):]
		}
		word.Reset()
		wordWidth = 0
	}
	for text != "" {
		cluster, cells := ansi.FirstGraphemeCluster(text, method)
		text = text[len(cluster):]
		switch {
		case cluster == "\n":
			flushWord()
			flushSpace()
			newline()
		case strings.TrimSpace(cluster) == "" && cluster != "\u00a0":
			flushWord()
			space.WriteString(cluster)
			spaceWidth += cells
		default:
			word.WriteString(cluster)
			wordWidth += cells
			if cluster == "-" {
				flushWord()
			}
		}
	}
	flushWord()
	flushSpace()
	return append(lines, line.String())
}
