package present

import "github.com/gboyke/lalabase-cli/internal/clean"

type Inline struct{ value string }
type Body struct{ value string }

func NewInline(raw string) Inline { return Inline{value: clean.Inline(raw)} }
func NewBody(raw string) Body     { return Body{value: clean.Text(raw)} }
func (s Inline) String() string   { return s.value }
func (s Body) String() string     { return s.value }
