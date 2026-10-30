package view

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// helpSeparator stands between two bindings of the help.
const helpSeparator = "  "

// HelpLines renders the bindings, as many per line as fit, on as many
// lines as it takes: an active binding is never cut away. A binding wider
// than the line stands alone and is clipped there. A key is drawn as a key
// cap, reversed with a space either side, its description faint right
// after it: the cap's own space parts the two, also where no attribute is
// drawn.
func HelpLines(bindings []present.KeyHelp, width int, method ansi.Method) []string {
	if width <= 0 || len(bindings) == 0 {
		return nil
	}
	var lines []string
	var line []string
	used := 0
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, strings.Join(line, helpSeparator))
		}
		line, used = nil, 0
	}
	for _, b := range bindings {
		label, description := " "+b.Key.String()+" ", b.Help.String()
		cells := method.StringWidth(label) + method.StringWidth(description)
		if len(line) > 0 && used+len(helpSeparator)+cells > width {
			flush()
		}
		if len(line) > 0 {
			used += len(helpSeparator)
		}
		used += cells
		if cells > width {
			line = append(line, fit(label+description, width, method))
			continue
		}
		line = append(line, capStyle.Render(label)+styled(description, present.Faint))
	}
	flush()
	return lines
}
