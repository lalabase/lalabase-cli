package present

import "github.com/gboyke/lalabase-cli/internal/clean"

// Different private fields also prevent conversion between Inline and Body.
type Inline struct{ inline string }
type Body struct{ body string }

func NewInline(raw string) Inline { return Inline{inline: clean.Inline(raw)} }
func NewBody(raw string) Body     { return Body{body: clean.Text(raw)} }
func (s Inline) String() string   { return s.inline }
func (s Body) String() string     { return s.body }
