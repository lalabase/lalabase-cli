package sourceguard

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		for _, finding := range checkFile(root, rel) {
			t.Error(finding)
		}
	}

	// Every directory that holds code, tests, data or CI must have been read:
	// a change that skips one turns this red instead of checking less.
	for _, dir := range []string{
		".", ".github/workflows", "cmd/lalabase", "internal/clean", "internal/clean/testdata",
		"internal/contract", "internal/contract/testdata", "internal/sourceguard", "tools/update-contract",
		"internal/api", "internal/cache", "internal/features", "internal/transport",
		"internal/claudecfg", "internal/profile", "internal/queue", "internal/safefile",
		"internal/ui", "internal/ui/view", "internal/present", "internal/uitext", "internal/terminal",
	} {
		if !seen[dir] {
			t.Errorf("no file of %s was checked", dir)
		}
	}
}

// binaryFiles are the tracked files that may hold any byte, by path. Empty:
// the repository has no binary file. A NUL byte anywhere else is a finding
// (it is a control character), because a shell still runs such a file and a
// diff shows it as binary, so nobody reads it in review.
var binaryFiles = map[string]bool{}

// checkFile checks one tracked file: it must be no symbolic link (a tool that
// writes into the repository would write through it), and unless it is on
// binaryFiles, every byte is read by scan.
func checkFile(root, rel string) []string {
	path := filepath.Join(root, rel)
	info, err := os.Lstat(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", rel, err)}
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return []string{fmt.Sprintf("%s is a symbolic link; the repository holds no links", rel)}
	}
	if binaryFiles[filepath.ToSlash(rel)] {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", rel, err)}
	}
	return scan(rel, data)
}

// scan returns one message per invisible or control character in a file, NUL
// included, and one per byte that is no UTF-8. Nothing switches it off for
// the rest of a file.
func scan(name string, data []byte) []string {
	var findings []string
	line := 1
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			findings = append(findings, fmt.Sprintf("%s:%d carries byte %#x, which is no UTF-8", name, line, data[i]))
		case r == '\n':
			line++
		case r == '\t':
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) || r == utf8.RuneError:
			findings = append(findings, fmt.Sprintf("%s:%d carries %U literally; write it as an escape", name, line, r))
		}
		i += size
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

// An invalid byte must not switch the check off for the rest of a file: a
// shell script with 0xff in a comment still runs, and a bidi override further
// down would hide behind it. The bad encoding is a finding of its own, and
// the rest of the file is still read.
func TestAnInvalidByteDoesNotHideTheRestOfTheFile(t *testing.T) {
	data := []byte("#!/bin/sh\n# caf\xff\necho \"a" + string(rune(0x202e)) + "b\"\n")
	got := scan("script.sh", data)
	if len(got) != 2 {
		t.Fatalf("want a finding for the invalid byte and one for U+202E, got %v", got)
	}
	if !strings.HasPrefix(got[0], "script.sh:2 ") || !strings.Contains(got[0], "UTF-8") {
		t.Errorf("first finding: %q", got[0])
	}
	if !strings.HasPrefix(got[1], "script.sh:3 ") || !strings.Contains(got[1], "U+202E") {
		t.Errorf("second finding: %q", got[1])
	}
}

// A NUL byte does not make a file binary here: a shell script with one in a
// comment still runs, and GitHub then shows the whole file as binary, so a
// reviewer cannot read it at all. The repository has no binary file; one
// that is needed later goes on binaryFiles by its path.
func TestANulByteIsAFindingAndTheRestIsStillRead(t *testing.T) {
	data := []byte("#!/bin/sh\n# x\x00y\necho \"a" + string(rune(0x202e)) + "b\"\n")
	got := scan("script.sh", data)
	if len(got) != 2 || !strings.HasPrefix(got[0], "script.sh:2 ") || !strings.HasPrefix(got[1], "script.sh:3 ") {
		t.Fatalf("want a finding for the NUL byte and one for U+202E, got %v", got)
	}
}

// A tracked symbolic link is a finding: a tool that writes into the
// repository would write through it to wherever it points.
func TestATrackedLinkIsAFinding(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "testdata")); err != nil {
		t.Fatal(err)
	}
	if got := checkFile(dir, "testdata"); len(got) != 1 || !strings.Contains(got[0], "link") {
		t.Fatalf("want one finding for the link, got %v", got)
	}
}

// trackedFiles lists the files git tracks plus new ones not yet added, so a
// file is checked before its first commit too.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.WaitDelay = time.Second
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
