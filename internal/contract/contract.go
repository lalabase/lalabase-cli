// Package contract holds the copy of the core's MCP contract
// (spec/fixtures/mcp_contract.json in lalabase/lalabase): the tools/list answer
// as a client sees it and one recorded answer per outcome for every tool and
// REST reader this program uses.
//
// Only tests import this package, so the binary does not carry the file. The
// copy is refreshed by hand when the client adopts a newer server version:
//
//	go run ./tools/update-contract -core ../lalabase -ref origin/main
//
// testdata/SOURCE.json records which core commit it came from and its checksums.
package contract

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed testdata/mcp_contract.json
var contractJSON []byte

//go:embed testdata/SOURCE.json
var sourceJSON []byte

// Source says where the copy came from.
type Source struct {
	CoreRepository   string `json:"core_repository"`
	CoreCommit       string `json:"core_commit"`
	CorePath         string `json:"core_path"`
	ContractSHA256   string `json:"contract_sha256"`
	FileSHA256       string `json:"file_sha256"`
	MCPServerVersion string `json:"mcp_server_version"`
}

// File is the whole contract file.
type File struct {
	Header struct {
		CoreCommit     string `json:"core_commit"`
		CoreDirty      bool   `json:"core_dirty"`
		ContractSHA256 string `json:"contract_sha256"`
	} `json:"header"`
	Contract Contract `json:"contract"`
}

// Contract is the compared payload.
type Contract struct {
	MCP struct {
		Initialize   Example                       `json:"initialize"`
		ToolsList    Example                       `json:"tools_list"`
		Unauthorized Example                       `json:"unauthorized"`
		Calls        map[string]map[string]Example `json:"calls"`
	} `json:"mcp"`
	REST map[string]map[string]Example `json:"rest"`
}

// Example is one recorded request and its answer. Token names which of the
// recorder's tokens asked (read, write, deactivated, invalid).
type Example struct {
	Token    string          `json:"token"`
	Request  json.RawMessage `json:"request"`
	Response struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	} `json:"response"`
}

// Tool is one entry of tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Raw returns the file as copied.
func Raw() []byte { return contractJSON }

// Load parses the contract file.
func Load() (*File, error) {
	var f File
	if err := json.Unmarshal(contractJSON, &f); err != nil {
		return nil, fmt.Errorf("contract: %w", err)
	}
	return &f, nil
}

// LoadSource parses SOURCE.json.
func LoadSource() (Source, error) {
	var s Source
	if err := json.Unmarshal(sourceJSON, &s); err != nil {
		return s, fmt.Errorf("contract source: %w", err)
	}
	return s, nil
}

// ServerVersion is the version the recorded initialize answer advertises.
func (f *File) ServerVersion() string {
	var body struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(f.Contract.MCP.Initialize.Response.Body, &body); err != nil {
		return ""
	}
	return body.Result.ServerInfo.Version
}

// Tools is the recorded tools/list, in wire order.
func (f *File) Tools() ([]Tool, error) {
	var body struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(f.Contract.MCP.ToolsList.Response.Body, &body); err != nil {
		return nil, fmt.Errorf("contract tools/list: %w", err)
	}
	return body.Result.Tools, nil
}
