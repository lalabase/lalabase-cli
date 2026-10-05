// Command update-contract copies the MCP contract from a checkout of the core
// repository into internal/contract/testdata and records where it came from.
//
//	go run ./tools/update-contract -core ../lalabase -ref origin/main
//
// It reads the file through `git show <ref>:<path>`, so the copy is exactly
// what that commit holds, not whatever the working tree happens to contain.
// The target is always internal/contract/testdata of the module it runs in;
// a symbolic link on that path, or as one of the two files, is refused
// rather than followed. The contract and SOURCE.json are replaced together or
// not at all.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	corePath     = "spec/fixtures/mcp_contract.json"
	targetDir    = "internal/contract/testdata"
	contractFile = "mcp_contract.json"
	sourceFile   = "SOURCE.json"
	modulePath   = "github.com/gboyke/lalabase-cli"
)

// rename is os.Rename; tests replace it to make one rename fail.
var rename = os.Rename

func main() {
	core := flag.String("core", "", "path to a checkout of lalabase/lalabase")
	ref := flag.String("ref", "origin/main", "commit or ref to copy from")
	flag.Parse()
	if *core == "" {
		fmt.Fprintln(os.Stderr, "update-contract: pass -core <path to a checkout of lalabase/lalabase>")
		os.Exit(2)
	}
	root, err := findModuleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "update-contract: %v\n", err)
		os.Exit(1)
	}
	summary, err := update(context.Background(), *core, *ref, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update-contract: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(summary)
}

// findModuleRoot walks up from the working directory to the go.mod of this
// module.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			if !strings.HasPrefix(string(data), "module "+modulePath+"\n") {
				return "", fmt.Errorf("%s/go.mod is not the module %s", dir, modulePath)
			}
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run it inside the lalabase-cli repository")
		}
		dir = parent
	}
}

// update copies the contract at ref from the core checkout into the target
// directory under root and writes SOURCE.json beside it.
func update(ctx context.Context, core, ref, root string) (string, error) {
	out, err := targetPath(root)
	if err != nil {
		return "", err
	}
	if err := checkTargets(out); err != nil {
		return "", err
	}

	commit, err := git(ctx, core, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	show, err := git(ctx, core, "show", commit+":"+corePath)
	if err != nil {
		return "", err
	}
	raw := []byte(show)

	source, err := describe(raw, commit)
	if err != nil {
		return "", fmt.Errorf("the contract at %s: %w", commit, err)
	}
	encoded, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return "", err
	}
	if err := replacePair(out, raw, append(encoded, '\n')); err != nil {
		return "", err
	}
	return fmt.Sprintf("contract %s (MCP %s) copied from %s",
		source["contract_sha256"][:12], source["mcp_server_version"], commit[:9]), nil
}

// describe checks the file and returns what SOURCE.json records about it. The
// payload must hash to the checksum its header names, the way the core
// computes it: SHA-256 of the compact JSON of "contract".
func describe(raw []byte, commit string) (map[string]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("no JSON: %w", err)
	}
	var header struct {
		ContractSHA256 string `json:"contract_sha256"`
	}
	if err := json.Unmarshal(top["header"], &header); err != nil || header.ContractSHA256 == "" {
		return nil, errors.New("no contract_sha256 in its header")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, top["contract"]); err != nil {
		return nil, fmt.Errorf("no contract payload: %w", err)
	}
	payloadSum := sha256.Sum256(compact.Bytes())
	if got := hex.EncodeToString(payloadSum[:]); got != header.ContractSHA256 {
		return nil, fmt.Errorf("payload hashes to %s, header says %s", got, header.ContractSHA256)
	}

	var version struct {
		MCP struct {
			Initialize struct {
				Response struct {
					Body struct {
						Result struct {
							ServerInfo struct {
								Version string `json:"version"`
							} `json:"serverInfo"`
						} `json:"result"`
					} `json:"body"`
				} `json:"response"`
			} `json:"initialize"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(top["contract"], &version); err != nil {
		return nil, err
	}

	fileSum := sha256.Sum256(raw)
	return map[string]string{
		"core_repository":    "lalabase/lalabase",
		"core_commit":        commit,
		"core_path":          corePath,
		"contract_sha256":    header.ContractSHA256,
		"file_sha256":        hex.EncodeToString(fileSum[:]),
		"mcp_server_version": version.MCP.Initialize.Response.Body.Result.ServerInfo.Version,
	}, nil
}

// targetPath returns root/internal/contract/testdata after checking every
// part below root: each must be a plain directory, never a link, since a
// write through a link would land outside the repository. Above root nothing
// is checked; where the checkout itself lives is the developer's choice.
func targetPath(root string) (string, error) {
	path := root
	for _, part := range strings.Split(targetDir, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%s is no plain directory; refusing to write through it", path)
		}
	}
	return path, nil
}

// checkTargets refuses a target file that is a link or anything but a
// regular file.
func checkTargets(out string) error {
	for _, name := range []string{contractFile, sourceFile} {
		info, err := os.Lstat(filepath.Join(out, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is no regular file; refusing to write through it", filepath.Join(out, name))
		}
	}
	return nil
}

// replacePair writes both files to temporary files in out first and renames
// them into place only when both are complete. A rename replaces a link or a
// read-only file itself, never what a link points to. Should the second rename
// fail, the old contract is put back, so the pair never disagrees.
func replacePair(out string, contract, source []byte) (err error) {
	tmpContract, err := writeTemp(out, contract)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpContract) }()
	tmpSource, err := writeTemp(out, source)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpSource) }()

	// Only a missing file means "no old contract". Any other read error stops
	// here, before anything is replaced: that contract could not be put back.
	contractPath := filepath.Join(out, contractFile)
	old, readErr := os.ReadFile(contractPath)
	hadOld := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("reading the current contract: %w", readErr)
	}

	if err := rename(tmpContract, contractPath); err != nil {
		return err
	}
	if err := rename(tmpSource, filepath.Join(out, sourceFile)); err != nil {
		if rerr := restoreContract(out, contractPath, old, hadOld); rerr != nil {
			return fmt.Errorf("writing %s failed (%w), and the old contract could not be put back (%w): %s now holds the new contract without a matching %s",
				sourceFile, err, rerr, contractPath, sourceFile)
		}
		return fmt.Errorf("writing %s failed, old contract restored: %w", sourceFile, err)
	}
	return nil
}

// restoreContract undoes the first rename: the old contract back in place, or
// the new one removed when there was none.
func restoreContract(out, contractPath string, old []byte, hadOld bool) error {
	if !hadOld {
		return os.Remove(contractPath)
	}
	tmp, err := writeTemp(out, old)
	if err != nil {
		return err
	}
	if err := rename(tmp, contractPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeTemp(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, ".update-contract-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}
