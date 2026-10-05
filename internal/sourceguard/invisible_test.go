package sourceguard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// No text file of the repository carries an invisible character literally: a
// bidi override or a zero-width space makes code, a workflow or a config read
// differently from what it does ("Trojan Source"), and this repository's own
// tests need exactly such characters as test input. They are written as
// escapes or numbers (0x202e), which read as what they are. Found on
// 2026-10-05: the tool that wrote the cleaning tests turned their escapes into
// the characters themselves, and this check caught the same in its own first
// version.
func TestTextFilesCarryNoInvisibleCharacters(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]bool{}
	for _, rel := range trackedFiles(t, root) {
		seen[filepath.ToSlash(filepath.Dir(rel))] = true
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range scan(rel, data) {
			t.Error(finding)
		}
	}

	// Every directory that holds code, tests, data or CI must have been read:
	// a change that skips one turns this red instead of checking less.
	for _, dir := range []string{
		".", ".github/workflows", "cmd/lalabase", "internal/clean", "internal/clean/testdata",
		"internal/contract", "internal/contract/testdata", "internal/sourceguard", "tools/update-contract",
	} {
		if !seen[dir] {
			t.Errorf("no file of %s was checked", dir)
		}
	}
}

// scan returns one message per invisible character in a text file. A file
// with a NUL byte or invalid UTF-8 is no text file and is skipped.
func scan(name string, data []byte) []string {
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil
	}
	var findings []string
	line := 1
	for _, r := range string(data) {
		switch {
		case r == '\n':
			line++
		case r == '\t':
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) || r == utf8.RuneError:
			findings = append(findings, fmt.Sprintf("%s:%d carries %U literally; write it as an escape", name, line, r))
		}
	}
	return findings
}

// The check finds what it is meant to find. The planted characters are built
// from numbers, so this file itself stays clean.
func TestTheScanReportsPlantedCharacters(t *testing.T) {
	for _, planted := range []rune{0x202e, 0x200b, 0x2028, 0x1b, 0xfffd} {
		data := []byte("package x\n\nvar s = \"a" + string(planted) + "b\"\n")
		if got := scan("planted.go", data); len(got) != 1 || !strings.HasPrefix(got[0], "planted.go:3 ") {
			t.Errorf("planted %U: got %v", planted, got)
		}
	}
	if got := scan("escaped.go", []byte("package x\n\tvar s = \"\\u202e\"\n")); len(got) != 0 {
		t.Errorf("an escape was reported: %v", got)
	}
}

// trackedFiles lists the files git tracks plus new ones not yet added, so a
// file is checked before its first commit too.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			continue // deleted in the working tree
		}
		files = append(files, name)
	}
	if len(files) < 10 {
		t.Fatalf("git listed %d files; the check is broken", len(files))
	}
	return files
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
