package present_test

import (
	"reflect"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/present"
)

// A dialog carries display values alone: an opaque value, a tone, a number
// or a flag. A plain string in it would be the one place where a ticket's
// title could reach the screen uncleaned (rule 1).
func TestADialogHoldsNoPlainString(t *testing.T) {
	checked := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		if checked[typ] {
			return
		}
		checked[typ] = true
		switch typ {
		case reflect.TypeFor[present.Inline](), reflect.TypeFor[present.Body](), reflect.TypeFor[present.Style]():
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := range typ.NumField() {
				f := typ.Field(i)
				walk(f.Type, path+"."+f.Name)
			}
		case reflect.Slice:
			walk(typ.Elem(), path+"[]")
		case reflect.Bool, reflect.Int:
		default:
			t.Errorf("%s is a %s, no display value", path, typ)
		}
	}
	walk(reflect.TypeFor[present.Dialog](), "Dialog")
	if !checked[reflect.TypeFor[present.DialogField]()] {
		t.Fatal("the walk did not reach the fields")
	}
}
