package present

// Dialog is a box over the screen (M1): a title in its top border, its
// fields, an error line and the keys that act. Display values only, like
// everything here: the title holds a ticket's title, the error line a
// server's text, the entries what a person wrote, and each is an Inline, so
// it was cleaned on its way in and cannot be forged.
type Dialog struct {
	Title Inline
	// Text is what the box says above its fields: a question has it and no
	// field.
	Text   Inline
	Fields []DialogField
	// Error is the line under the fields: what stopped the send, or what
	// is going on; empty for none, and the line stays.
	Error      Inline
	ErrorStyle Style
	Help       []KeyHelp
	// Large is the size of one field of several lines, about 80 % of the
	// screen; else one or a few fields of one line.
	Large bool
}

// DialogField is one input field: its label, the lines of its entry in
// sight (the caret among them, in the active one), and a faint hint.
type DialogField struct {
	Label  Inline
	Entry  []Inline
	Hint   Inline
	Active bool
}
