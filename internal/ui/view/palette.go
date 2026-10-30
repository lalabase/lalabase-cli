package view

import "charm.land/lipgloss/v2"

// The client's own palette, the same on every terminal: dark ground, pale
// text, one tone per meaning. Ground and text are set for the whole window
// (Model.View, by the terminal's own colours), so a space needs no
// attribute and a plain word none either. Everything else stays an
// attribute over them where one exists: faint is the terminal's faint of
// the text, a bar the terminal's reverse with the bar's colour as its
// foreground. NO_COLOR then takes the colours and keeps the weight.
var (
	GroundColor = lipgloss.Color("#0e1014")
	TextColor   = lipgloss.Color("#c4c7cd")

	brightColor = lipgloss.Color("#f1f3f5")
	barColor    = lipgloss.Color("#d8dadf")
	restColor   = lipgloss.Color("#3b3f46") // a cursor where the keys do not act
	lineColor   = lipgloss.Color("#6a6e78") // a frame: drawn faint, so #3b3f46 on the ground

	focusColor  = lipgloss.Color("#6b9aee")
	brandColor  = lipgloss.Color("#79c2fa")
	authorColor = lipgloss.Color("#bf94e4")
	cyanColor   = lipgloss.Color("#75c5d5")
	// The steps of a waiting agent's pulse down from authorColor.
	authorSoftColor = lipgloss.Color("#a582c6")
	authorDimColor  = lipgloss.Color("#8b70a8")
	yellowColor     = lipgloss.Color("#dfc184")
	greenColor      = lipgloss.Color("#9dcf92")
	redColor        = lipgloss.Color("#df7376")
)

// focusGround is the focused pane's background, a little lighter than the
// window's. It is written as a sequence: onFocusGround puts it back after
// every reset inside a line.
const focusGround = "\x1b[48;2;24;27;33m"

// quietLine is the style of what only parts a screen: frames out of focus,
// the rule beside the meta bar, a scrollbar's track.
var quietLine = lipgloss.NewStyle().Faint(true).Foreground(lineColor)

// capStyle is a filled cell run, a key cap or the focused bar: reversed, so
// its foreground is the fill and the window's ground its text.
var capStyle = lipgloss.NewStyle().Reverse(true).Foreground(barColor)
