package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal core repository with one committed contract file.
func coreRepo(t *testing.T, contract string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	path := filepath.Join(dir, filepath.FromSlash(corePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contract), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "contract")
	return dir, git("rev-parse", "HEAD")
}

// A contract whose header carries the checksum of its payload, as the core
// writes it.
func validContract(t *testing.T) string {
	t.Helper()
	payload := `{"mcp":{"initialize":{"response":{"body":{"result":{"serverInfo":{"version":"0.7.0"}}}}}}}`
	sum := sha256.Sum256([]byte(payload))
	return `{"header":{"contract_sha256":"` + hex.EncodeToString(sum[:]) + `"},"contract":` + payload + "}\n"
}

// A module root with the target directory in place; returns root and target.
func moduleRoot(t *testing.T) (root, out string) {
	t.Helper()
	root = t.TempDir()
	out = filepath.Join(root, filepath.FromSlash(targetDir))
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	return root, out
}

func TestUpdateWritesTheContractAndItsSource(t *testing.T) {
	contract := validContract(t)
	core, commit := coreRepo(t, contract)
	root, out := moduleRoot(t)

	if _, err := update(context.Background(), core, "HEAD", root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, contractFile))
	if err != nil || string(got) != contract {
		t.Fatalf("contract not copied byte for byte: %v %q", err, got)
	}
	var source map[string]string
	raw, err := os.ReadFile(filepath.Join(out, sourceFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(contract))
	if source["core_commit"] != commit || source["file_sha256"] != hex.EncodeToString(sum[:]) || source["mcp_server_version"] != "0.7.0" {
		t.Fatalf("SOURCE.json does not describe the copy: %v", source)
	}
}

// Both files or neither: a refused update leaves the old pair as it was.
func assertUnchanged(t *testing.T, out string, before map[string][]byte) {
	t.Helper()
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s changed after a refused update", name)
		}
	}
}

func existingPair(t *testing.T) (root, out string, before map[string][]byte) {
	t.Helper()
	root, out = moduleRoot(t)
	before = map[string][]byte{contractFile: []byte("old contract\n"), sourceFile: []byte("old source\n")}
	for name, data := range before {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, out, before
}

func TestAPayloadThatDoesNotMatchItsChecksumIsRefused(t *testing.T) {
	core, _ := coreRepo(t, strings.Replace(validContract(t), "0.7.0", "0.7.1", 1))
	root, out, before := existingPair(t)
	if _, err := update(context.Background(), core, "HEAD", root); err == nil {
		t.Fatal("a tampered payload was accepted")
	}
	assertUnchanged(t, out, before)
}

func TestATargetThatIsALinkIsRefused(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	for _, name := range []string{contractFile, sourceFile} {
		t.Run(name, func(t *testing.T) {
			root, out, before := existingPair(t)
			outside := filepath.Join(t.TempDir(), "outside.json")
			if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(out, name)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(out, name)); err != nil {
				t.Fatal(err)
			}
			delete(before, name)

			if _, err := update(context.Background(), core, "HEAD", root); err == nil {
				t.Fatal("a link as target was accepted")
			}
			if got, _ := os.ReadFile(outside); string(got) != "outside\n" {
				t.Fatalf("the file behind the link was overwritten: %q", got)
			}
			assertUnchanged(t, out, before)
		})
	}
}

// Every part of the path from the module root down is checked, not only the
// last one: a link committed as internal/contract/testdata, or as one of its
// parents, would send the write elsewhere.
func TestALinkAnywhereOnThePathIsRefused(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	for _, linked := range []string{"internal", "internal/contract", targetDir} {
		t.Run(linked, func(t *testing.T) {
			root := t.TempDir()
			elsewhere := t.TempDir()
			if err := os.MkdirAll(filepath.Join(elsewhere, "contract", "testdata"), 0o750); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Dir(filepath.Join(root, filepath.FromSlash(linked)))
			if err := os.MkdirAll(parent, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(elsewhere, "testdata"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(root, filepath.FromSlash(linked))); err != nil {
				t.Fatal(err)
			}
			if _, err := update(context.Background(), core, "HEAD", root); err == nil {
				t.Fatal("a linked path was accepted")
			}
			found := false
			_ = filepath.WalkDir(elsewhere, func(path string, d fs.DirEntry, _ error) error {
				if d != nil && !d.IsDir() {
					found = true
				}
				return nil
			})
			if found {
				t.Fatal("a file was written through the link")
			}
		})
	}
}

// A write that fails leaves neither a half pair nor temporary files.
func TestAFailedWriteLeavesThePairAsItWas(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out, before := existingPair(t)
	if err := os.Chmod(out, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(out, 0o700) })

	if _, err := update(context.Background(), core, "HEAD", root); err == nil {
		t.Fatal("an unwritable directory did not fail")
	}
	assertUnchanged(t, out, before)
	entries, _ := os.ReadDir(out)
	if len(entries) != 2 {
		t.Fatalf("leftovers in the target: %v", entries)
	}
}

// A read-only SOURCE.json (the reviewer's case) is replaced together with the
// contract, not left behind with the old checksum.
func TestAReadOnlySourceIsReplacedWithTheContract(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out, _ := existingPair(t)
	if err := os.Chmod(filepath.Join(out, sourceFile), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := update(context.Background(), core, "HEAD", root); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, sourceFile))
	if !strings.Contains(string(raw), "file_sha256") {
		t.Fatalf("SOURCE.json was not replaced: %q", raw)
	}
}

func TestAnUnknownRefWritesNothing(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out, before := existingPair(t)
	if _, err := update(context.Background(), core, "no-such-ref", root); err == nil {
		t.Fatal("an unknown ref did not fail")
	}
	assertUnchanged(t, out, before)
}

// failRename makes the rename into SOURCE.json fail, and with failRestore
// also the rename that puts the old contract back.
func failRename(t *testing.T, failRestore bool) {
	t.Helper()
	saved := rename
	t.Cleanup(func() { rename = saved })
	contractRenames := 0
	rename = func(from, to string) error {
		switch filepath.Base(to) {
		case sourceFile:
			return errors.New("injected: SOURCE.json not writable")
		case contractFile:
			contractRenames++
			if failRestore && contractRenames == 2 {
				return errors.New("injected: restore failed")
			}
		}
		return saved(from, to)
	}
}

func TestASecondRenameThatFailsPutsTheOldContractBack(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out, before := existingPair(t)
	failRename(t, false)

	_, err := update(context.Background(), core, "HEAD", root)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("want an error saying the contract was restored, got %v", err)
	}
	assertUnchanged(t, out, before)
}

func TestASecondRenameThatFailsWithoutAnOldContractRemovesTheNewOne(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out := moduleRoot(t)
	failRename(t, false)

	if _, err := update(context.Background(), core, "HEAD", root); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(out, contractFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a contract without SOURCE.json was left behind: %v", err)
	}
}

// When putting the old contract back fails too, the error says so instead of
// claiming it was restored.
func TestAFailedRestoreIsReported(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, _, _ := existingPair(t)
	failRename(t, true)

	_, err := update(context.Background(), core, "HEAD", root)
	if err == nil || strings.Contains(err.Error(), "restored") || !strings.Contains(err.Error(), "could not be put back") {
		t.Fatalf("want an error saying the restore failed, got %v", err)
	}
}

// An existing contract that cannot be read stops the update before anything
// is replaced: it could not be put back.
func TestAnUnreadableContractStopsTheUpdate(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	root, out, before := existingPair(t)
	path := filepath.Join(out, contractFile)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("running with rights that read a mode-000 file")
	}

	if _, err := update(context.Background(), core, "HEAD", root); err == nil {
		t.Fatal("an unreadable contract did not stop the update")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(t, out, before)
}
