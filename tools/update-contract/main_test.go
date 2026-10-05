package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestUpdateWritesTheContractAndItsSource(t *testing.T) {
	contract := validContract(t)
	core, commit := coreRepo(t, contract)
	out := t.TempDir()

	if _, err := update(context.Background(), core, "HEAD", out); err != nil {
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

func existingPair(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	out := t.TempDir()
	before := map[string][]byte{contractFile: []byte("old contract\n"), sourceFile: []byte("old source\n")}
	for name, data := range before {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return out, before
}

func TestAPayloadThatDoesNotMatchItsChecksumIsRefused(t *testing.T) {
	core, _ := coreRepo(t, strings.Replace(validContract(t), "0.7.0", "0.7.1", 1))
	out, before := existingPair(t)
	if _, err := update(context.Background(), core, "HEAD", out); err == nil {
		t.Fatal("a tampered payload was accepted")
	}
	assertUnchanged(t, out, before)
}

func TestATargetThatIsALinkIsRefused(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	for _, name := range []string{contractFile, sourceFile} {
		t.Run(name, func(t *testing.T) {
			out, before := existingPair(t)
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

			if _, err := update(context.Background(), core, "HEAD", out); err == nil {
				t.Fatal("a link as target was accepted")
			}
			if got, _ := os.ReadFile(outside); string(got) != "outside\n" {
				t.Fatalf("the file behind the link was overwritten: %q", got)
			}
			assertUnchanged(t, out, before)
		})
	}
}

func TestATargetDirectoryThatIsALinkIsRefused(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "out")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := update(context.Background(), core, "HEAD", link); err == nil {
		t.Fatal("a linked target directory was accepted")
	}
}

// A write that fails leaves neither a half pair nor temporary files.
func TestAFailedWriteLeavesThePairAsItWas(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	out, before := existingPair(t)
	if err := os.Chmod(out, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(out, 0o700) })

	if _, err := update(context.Background(), core, "HEAD", out); err == nil {
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
	out, _ := existingPair(t)
	if err := os.Chmod(filepath.Join(out, sourceFile), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := update(context.Background(), core, "HEAD", out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, sourceFile))
	if !strings.Contains(string(raw), "file_sha256") {
		t.Fatalf("SOURCE.json was not replaced: %q", raw)
	}
}

func TestAnUnknownRefWritesNothing(t *testing.T) {
	core, _ := coreRepo(t, validContract(t))
	out, before := existingPair(t)
	if _, err := update(context.Background(), core, "no-such-ref", out); err == nil {
		t.Fatal("an unknown ref did not fail")
	}
	assertUnchanged(t, out, before)
}
