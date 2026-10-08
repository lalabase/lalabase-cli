package terminal

import (
	"reflect"
	"testing"
)

func TestUTF8LocalePrecedence(t *testing.T) {
	for _, tc := range []struct {
		all, ctype, lang string
		want             bool
		read             []string
	}{
		{"UTF-8", "C", "C", true, []string{"LC_ALL"}},
		{"C", "UTF-8", "UTF-8", false, []string{"LC_ALL"}},
		{"", "utf8", "C", true, []string{"LC_ALL", "LC_CTYPE"}},
		{"", "POSIX", "UTF-8", false, []string{"LC_ALL", "LC_CTYPE"}},
		{"", "", "en_US.UTF-8", true, []string{"LC_ALL", "LC_CTYPE", "LANG"}},
		{"", "", "", false, []string{"LC_ALL", "LC_CTYPE", "LANG"}},
		{" ", "UTF-8", "UTF-8", false, []string{"LC_ALL"}},
	} {
		var read []string
		values := map[string]string{"LC_ALL": tc.all, "LC_CTYPE": tc.ctype, "LANG": tc.lang}
		got := UTF8(func(k string) string { read = append(read, k); return values[k] })
		if got != tc.want || !reflect.DeepEqual(read, tc.read) {
			t.Fatalf("%v: got %v, read %v; want %v, %v", values, got, read, tc.want, tc.read)
		}
	}
}

func TestUTF8LocaleSpellings(t *testing.T) {
	for _, value := range []string{"UTF-8", "utf8", "C.UTF-8", "C.utf8", "en_US.UTF-8", "en_US.UTF-8@euro", "de_DE.utf8"} {
		if !UTF8(func(string) string { return value }) {
			t.Errorf("refused %q", value)
		}
	}
	for _, value := range []string{"", "C", "POSIX", "en_US", "UTF-16", "en_US.ISO-8859-1", "en_US.UTF-8junk", " UTF8", "UTF8 ", "en.UTF8@", "en.UTF8@bad!", "en..UTF8", "\x1bUTF8", "en.UTF8\n", "garbage"} {
		if UTF8(func(string) string { return value }) {
			t.Errorf("accepted %q", value)
		}
	}
}
