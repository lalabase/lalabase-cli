package sourceguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// No Go file carries an invisible character literally: a bidi override or a
// zero-width space in a source file makes the code read differently from what
// it does ("Trojan Source"), and this repository's own tests need exactly
// such characters as test input. They are written as escapes (\u202e), which
// read as what they are. Found on 2026-10-05: the tool that wrote the cleaning
// tests turned their escapes into the characters themselves, and this check
// caught the same in its own first version.
func TestGoSourcesCarryNoInvisibleCharacters(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		line := 1
		for _, r := range string(data) {
			if r == '\n' {
				line++
				continue
			}
			if r == '\t' {
				continue
			}
			if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) || r == '\ufffd' {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d carries %U literally; write it as an escape", rel, line, r)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 5 {
		t.Fatalf("only %d Go files found under %s; the walk is broken", checked, root)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}
