package present_test

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/gboyke/lalabase-cli/internal/present"
)

func TestPresentationConstructorsCleanEveryField(t *testing.T) {
	for _, tc := range []struct{ raw, inline, body string }{
		{"a\n\tb\r\nc", "a b c", "a\n\tb \nc"},
		{"a\x1b[2Jb\x07", "a [2Jb ", "a [2Jb "},
		{"x\u202ey\u2066z\u2069", "xyz", "xyz"},
		{"x\xffy", "x\ufffdy", "x\ufffdy"},
		{"x\u009by", "x y", "x y"},
		{"\u00db", "\u00db", "\u00db"},
	} {
		i, b := present.NewInline(tc.raw).String(), present.NewBody(tc.raw).String()
		if i != tc.inline || b != tc.body {
			t.Errorf("%q: inline %q, body %q; want %q, %q", tc.raw, i, b, tc.inline, tc.body)
		}
		if !utf8.ValidString(i) || !utf8.ValidString(b) {
			t.Fatal("invalid UTF-8")
		}
	}
}

func TestPresentationTypesCannotBeForged(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[present.Inline](), reflect.TypeFor[present.Body]()} {
		if typ.Kind() != reflect.Struct {
			t.Fatalf("%s must be a struct, got %s", typ.Name(), typ.Kind())
		}
		if typ.NumField() != 1 || typ.Field(0).IsExported() {
			t.Fatalf("%s must have exactly one private field", typ.Name())
		}
	}
}
