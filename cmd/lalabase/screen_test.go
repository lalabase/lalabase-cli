package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// vscreen is a small terminal screen for the PTY tests: the renderer
// writes only the cells that changed (a cursor move and one character),
// so what a person sees is the screen, not the byte stream. It knows the
// cursor moves, erasing, inserting, deleting and scrolling the renderer
// uses; everything else (styles, modes, queries) changes no cell.
type vscreen struct {
	rows, cols  int
	cells       [][]string
	row, col    int
	top, bottom int
}

func newScreen(rows, cols int) *vscreen {
	s := &vscreen{rows: rows, cols: cols, bottom: rows - 1}
	s.cells = make([][]string, rows)
	for i := range s.cells {
		s.cells[i] = blankLine(cols)
	}
	return s
}

func blankLine(cols int) []string {
	line := make([]string, cols)
	for i := range line {
		line[i] = " "
	}
	return line
}

// renderScreen plays the output into a screen of the size and returns its
// lines, trailing blanks trimmed.
func renderScreen(output string, rows, cols int) []string {
	s := newScreen(rows, cols)
	p := ansi.NewParser()
	p.SetHandler(ansi.Handler{
		Print:   s.put,
		Execute: s.execute,
		HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
			if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
				return
			}
			s.csi(cmd.Final(), params)
		},
	})
	p.Parse([]byte(output))
	lines := make([]string, rows)
	for i, line := range s.cells {
		lines[i] = strings.TrimRight(strings.Join(line, ""), " ")
	}
	return lines
}

func (s *vscreen) put(r rune) {
	w := ansi.StringWidth(string(r))
	if w == 0 {
		if s.col > 0 && s.col <= s.cols {
			s.cells[s.row][s.col-1] += string(r)
		}
		return
	}
	if s.col >= s.cols {
		return // the renderer never relies on autowrap
	}
	s.cells[s.row][s.col] = string(r)
	if w == 2 && s.col+1 < s.cols {
		s.cells[s.row][s.col+1] = ""
	}
	s.col = min(s.cols, s.col+w)
}

func (s *vscreen) execute(b byte) {
	switch b {
	case '\r':
		s.col = 0
	case '\n':
		if s.row == s.bottom {
			s.scrollUp(1)
		} else if s.row < s.rows-1 {
			s.row++
		}
	case '\b':
		s.col = max(0, s.col-1)
	case '\t':
		s.col = min(s.cols-1, (s.col/8+1)*8)
	}
}

func (s *vscreen) param(params ansi.Params, i, def int) int {
	if i < len(params) {
		if v := params[i].Param(def); v > 0 {
			return v
		}
	}
	return def
}

func (s *vscreen) csi(final byte, params ansi.Params) {
	n := s.param(params, 0, 1)
	switch final {
	case 'H', 'f':
		s.row, s.col = s.clampRow(s.param(params, 0, 1)-1), s.clampCol(s.param(params, 1, 1)-1)
	case 'A':
		s.row = s.clampRow(s.row - n)
	case 'B':
		s.row = s.clampRow(s.row + n)
	case 'C':
		s.col = s.clampCol(s.col + n)
	case 'D':
		s.col = s.clampCol(s.col - n)
	case 'E':
		s.row, s.col = s.clampRow(s.row+n), 0
	case 'F':
		s.row, s.col = s.clampRow(s.row-n), 0
	case 'G':
		s.col = s.clampCol(n - 1)
	case 'd':
		s.row = s.clampRow(n - 1)
	case 'J':
		switch s.param(params, 0, 0) {
		case 0:
			s.eraseLine(s.col, s.cols)
			for r := s.row + 1; r < s.rows; r++ {
				s.cells[r] = blankLine(s.cols)
			}
		case 1:
			s.eraseLine(0, s.col+1)
			for r := 0; r < s.row; r++ {
				s.cells[r] = blankLine(s.cols)
			}
		default:
			for r := range s.cells {
				s.cells[r] = blankLine(s.cols)
			}
		}
	case 'K':
		switch s.param(params, 0, 0) {
		case 0:
			s.eraseLine(s.col, s.cols)
		case 1:
			s.eraseLine(0, s.col+1)
		default:
			s.eraseLine(0, s.cols)
		}
	case 'X':
		s.eraseLine(s.col, s.col+n)
	case 'P':
		line := s.cells[s.row]
		rest := append([]string{}, line[min(s.cols, s.col+n):]...)
		copy(line[s.col:], rest)
		for i := s.col + len(rest); i < s.cols; i++ {
			line[i] = " "
		}
	case '@':
		line := s.cells[s.row]
		rest := append([]string{}, line[s.col:max(s.col, s.cols-n)]...)
		for i := s.col; i < min(s.cols, s.col+n); i++ {
			line[i] = " "
		}
		copy(line[min(s.cols, s.col+n):], rest)
	case 'L':
		for range n {
			s.insertLine(s.row)
		}
	case 'M':
		for range n {
			s.deleteLine(s.row)
		}
	case 'S':
		s.scrollUp(n)
	case 'T':
		s.scrollDown(n)
	case 'r':
		s.top, s.bottom = s.clampRow(s.param(params, 0, 1)-1), s.clampRow(s.param(params, 1, s.rows)-1)
		if s.top > s.bottom {
			s.top, s.bottom = 0, s.rows-1
		}
		s.row, s.col = 0, 0
	}
}

func (s *vscreen) clampRow(r int) int { return max(0, min(s.rows-1, r)) }
func (s *vscreen) clampCol(c int) int { return max(0, min(s.cols-1, c)) }

func (s *vscreen) eraseLine(from, to int) {
	for i := max(0, from); i < min(s.cols, to); i++ {
		s.cells[s.row][i] = " "
	}
}

func (s *vscreen) scrollUp(n int) {
	for range n {
		s.deleteLine(s.top)
	}
}

func (s *vscreen) scrollDown(n int) {
	for range n {
		s.insertLine(s.top)
	}
}

// insertLine inserts a blank line at row inside the scrolling region; the
// region's last line falls off.
func (s *vscreen) insertLine(row int) {
	if row < s.top || row > s.bottom {
		return
	}
	copy(s.cells[row+1:s.bottom+1], s.cells[row:s.bottom])
	s.cells[row] = blankLine(s.cols)
}

// deleteLine removes the line at row inside the scrolling region; a blank
// line comes in at the region's end.
func (s *vscreen) deleteLine(row int) {
	if row < s.top || row > s.bottom {
		return
	}
	copy(s.cells[row:s.bottom], s.cells[row+1:s.bottom+1])
	s.cells[s.bottom] = blankLine(s.cols)
}

// screenHas reports whether one line of the screen holds every marker.
func screenHas(lines []string, markers ...string) bool {
	for _, m := range markers {
		found := false
		for _, line := range lines {
			if strings.Contains(line, m) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
