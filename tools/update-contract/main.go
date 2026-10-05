// Command update-contract copies the MCP contract from a checkout of the core
// repository into internal/contract/testdata and records where it came from.
//
//	go run ./tools/update-contract -core ../lalabase -ref origin/main
//
// It reads the file through `git show <ref>:<path>`, so the copy is exactly
// what that commit holds, not whatever the working tree happens to contain.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const corePath = "spec/fixtures/mcp_contract.json"

func main() {
	core := flag.String("core", "", "path to a checkout of lalabase/lalabase")
	ref := flag.String("ref", "origin/main", "commit or ref to copy from")
	out := flag.String("out", "internal/contract/testdata", "target directory")
	flag.Parse()
	if *core == "" {
		fail("pass -core <path to a checkout of lalabase/lalabase>")
	}

	commit := strings.TrimSpace(run(*core, "rev-parse", *ref+"^{commit}"))
	raw := []byte(run(*core, "show", commit+":"+corePath))

	var file struct {
		Header struct {
			ContractSHA256 string `json:"contract_sha256"`
		} `json:"header"`
		Contract struct {
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
		} `json:"contract"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		fail("the contract at %s is no JSON: %v", commit, err)
	}
	if file.Header.ContractSHA256 == "" {
		fail("the contract at %s has no contract_sha256 in its header", commit)
	}

	sum := sha256.Sum256(raw)
	source := map[string]string{
		"core_repository":    "lalabase/lalabase",
		"core_commit":        commit,
		"core_path":          corePath,
		"contract_sha256":    file.Header.ContractSHA256,
		"file_sha256":        hex.EncodeToString(sum[:]),
		"mcp_server_version": file.Contract.MCP.Initialize.Response.Body.Result.ServerInfo.Version,
	}
	encoded, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		fail("%v", err)
	}
	write(filepath.Join(*out, "mcp_contract.json"), raw)
	write(filepath.Join(*out, "SOURCE.json"), append(encoded, '\n'))
	fmt.Printf("contract %s (MCP %s) copied from %s\n", file.Header.ContractSHA256[:12], source["mcp_server_version"], commit[:9])
}

func run(dir string, args ...string) string {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		fail("git %s: %v %s", strings.Join(args, " "), err, stderr.String())
	}
	return string(output)
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "update-contract: "+format+"\n", args...)
	os.Exit(1)
}
