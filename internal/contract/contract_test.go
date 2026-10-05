package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"
)

// The copy is the core's file, byte for byte: a hand edit here would make the
// compatibility tests check a contract no server ever answered.
func TestTheCopyIsTheFileTheCoreWrote(t *testing.T) {
	src, err := LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(Raw())
	if got := hex.EncodeToString(sum[:]); got != src.FileSHA256 {
		t.Fatalf("testdata/mcp_contract.json has sha256 %s, SOURCE.json records %s; copy it again with tools/update-contract", got, src.FileSHA256)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(src.CoreCommit) {
		t.Fatalf("SOURCE.json core_commit %q is no full commit id", src.CoreCommit)
	}
}

func TestHeaderAndSourceAgree(t *testing.T) {
	file, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	src, err := LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	if file.Header.ContractSHA256 != src.ContractSHA256 {
		t.Fatalf("header contract_sha256 %s, SOURCE.json %s", file.Header.ContractSHA256, src.ContractSHA256)
	}
	if v := file.ServerVersion(); v != src.MCPServerVersion {
		t.Fatalf("contract advertises %q, SOURCE.json records %q", v, src.MCPServerVersion)
	}
}

func TestTheContractHasWhatTheClientReads(t *testing.T) {
	file, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tools, err := file.Tools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) == 0 {
		t.Fatal("tools/list is empty")
	}
	for _, name := range []string{"list_notifications", "mark_notification_read", "list_tickets", "get_ticket", "post_comment", "search_knowledge"} {
		if len(file.Contract.MCP.Calls[name]) == 0 {
			t.Errorf("no example answers for %s", name)
		}
	}
	for _, path := range []string{"GET /api/v1/me", "GET /api/v1/organisations", "GET /api/v1/projects"} {
		if len(file.Contract.REST[path]) == 0 {
			t.Errorf("no example answers for %s", path)
		}
	}
}
