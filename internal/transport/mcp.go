package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

const (
	protocolVersion = "2025-06-18"
	// maxToolPages bounds tools/list paging: a server handing out cursors
	// forever does not keep the client busy.
	maxToolPages = 20
	// The JSON-RPC code for invalid params, which the MCP SDK also answers an
	// unknown tool with.
	rpcInvalidParams = -32602

	metaKind   = "lalabase/kind"
	metaCode   = "lalabase/code"
	metaReason = "lalabase/reason"
)

// codeClasses maps the machine field of a tool error (MCP 0.7.0 on) to a
// class. A code not listed here is a refusal.
var codeClasses = map[string]Class{
	"invalid_input": InvalidInput,
	"read_only":     ReadOnly,
	"not_found":     NotFound,
	"forbidden":     NoAccess,
	"refused":       Refused,
}

// legacyErrors are the complete error texts of a server before 0.7.0 that
// say more than "refused". Only an exact match counts: a text that merely
// starts like one could be anything.
var legacyErrors = map[string]Class{
	"Project not found or not accessible.":               NotFound,
	"Not authorized.":                                    NoAccess,
	"This token is read-only; writes are not permitted.": ReadOnly,
}

// ServerInfo is what initialize names.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool is one entry of tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Properties returns the argument names the tool's input schema declares.
func (t Tool) Properties() map[string]json.RawMessage {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(t.InputSchema, &schema)
	if schema.Properties == nil {
		return map[string]json.RawMessage{}
	}
	return schema.Properties
}

// Call is one tools/call.
type Call struct {
	Tool string
	Args map[string]any
	// OneRecord: the tool addresses one record, so an empty answer means the
	// record was not found. For every other tool an empty answer is a result.
	OneRecord bool
}

// legacyNotFound are the exact texts a server before 0.7.0 answers a missing
// record of a call with, naming the id or number that was asked for. Kept
// here beside legacyErrors: every sentence the client reads of an older
// server stands in one place. update_ticket is listed for completeness
// (its sentence is get_ticket's): a server that has the tool (0.10.0 on)
// always sends the machine field. What a write's "not found" does is the
// cache's decision (cache.Decide): the record stays, a reload decides.
func legacyNotFound(call Call) []string {
	if !call.OneRecord {
		return nil
	}
	var texts []string
	switch call.Tool {
	case "get_ticket", "post_comment", "update_ticket":
		if id, ok := call.Args["ticket_id"].(string); ok {
			texts = append(texts, "No ticket found for "+strings.TrimSpace(id)+".")
		}
		if n, ok := ticketNumber(call.Args["number"]); ok {
			texts = append(texts, "No ticket found for number "+strconv.Itoa(n)+".")
		}
	case "mark_notification_read":
		if id, ok := call.Args["notification_id"].(string); ok {
			texts = append(texts, "No notification found for "+strings.TrimSpace(id)+".")
		}
	}
	return texts
}

// ticketNumber reads a ticket number the way the server did (TicketNumber
// in the core): spaces and one leading "#" go, the rest is a positive
// integer. The server's sentence names that integer, so "#187" and "0187"
// both come back as 187.
func ticketNumber(v any) (int, bool) {
	text, ok := v.(string)
	if !ok {
		return 0, false
	}
	text = strings.TrimPrefix(strings.TrimSpace(text), "#")
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// Initialize asks the server who it is.
func (c *Client) Initialize(ctx context.Context) (ServerInfo, Answer) {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "lalabase", "version": c.version},
	}
	result, ans := c.rpc(ctx, "initialize", params)
	if !ans.OK() {
		return ServerInfo{}, ans
	}
	var body struct {
		ServerInfo ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(result, &body); err != nil || body.ServerInfo.Name == "" {
		return ServerInfo{}, Answer{Class: Protocol, Status: ans.Status, Message: "initialize names no server", Sent: true}
	}
	return body.ServerInfo, ans
}

// ListTools reads the whole tools/list, following its cursor.
func (c *Client) ListTools(ctx context.Context) ([]Tool, Answer) {
	var tools []Tool
	cursor := ""
	for range maxToolPages {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, ans := c.rpc(ctx, "tools/list", params)
		if !ans.OK() {
			return nil, ans
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, Answer{Class: Protocol, Status: ans.Status, Message: "tools/list is no list", Err: err, Sent: true}
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, ans
		}
		cursor = page.NextCursor
	}
	return nil, Answer{Class: Protocol, Status: http.StatusOK, Message: "tools/list did not end", Sent: true}
}

// CallTool runs one tool and classifies its answer.
func (c *Client) CallTool(ctx context.Context, call Call) Answer {
	args := call.Args
	if args == nil {
		args = map[string]any{}
	}
	result, ans := c.rpc(ctx, "tools/call", map[string]any{"name": call.Tool, "arguments": args})
	if !ans.OK() {
		return ans
	}

	var body struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool           `json:"isError"`
		Meta    map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(result, &body); err != nil {
		return Answer{Class: Protocol, Status: ans.Status, Message: "a tool answer of the wrong shape", Err: err, Sent: true}
	}
	i := slices.IndexFunc(body.Content, func(c struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}) bool {
		return c.Type == "text"
	})
	if i < 0 {
		return Answer{Class: Protocol, Status: ans.Status, Message: "a tool answer without text", Sent: true}
	}
	text := body.Content[i].Text
	kind, _ := body.Meta[metaKind].(string)
	code, _ := body.Meta[metaCode].(string)
	reason, _ := body.Meta[metaReason].(string)
	out := Answer{Status: ans.Status, Code: code, Sent: true}

	switch {
	// A machine code means an error, with or without isError. A code next to
	// a kind, or a kind the client does not know, contradicts itself and is a
	// refusal: nothing a server says unclearly may drop a cached entry.
	case code != "" && kind != "", kind != "" && kind != "ok" && kind != "empty":
		out.Class, out.Message = Refused, text
	case code != "":
		out.Class, out.Message = Refused, text
		if class, ok := codeClasses[code]; ok {
			out.Class = class
		}
		if out.Class == Refused {
			out.Reason = reason
		}
	case body.IsError:
		out.Class, out.Message = Refused, text
		if class, ok := legacyErrors[text]; ok {
			out.Class = class
		}
	case call.OneRecord && kind == "empty":
		out.Class, out.Message = NotFound, text
	case call.OneRecord && kind == "" && slices.Contains(legacyNotFound(call), text):
		out.Class, out.Message = NotFound, text
	// An older server answers objects as JSON and missing records with the
	// sentences above; any other plain text from it is a refusal. A newer
	// server that marks a text "ok" is believed: the code tools answer
	// Markdown on purpose.
	case kind == "" && !json.Valid([]byte(text)):
		out.Class, out.Message = Refused, text
	default:
		out.Class, out.Payload = Object, json.RawMessage(text)
	}
	return out
}

// rpc sends one JSON-RPC request and returns its result, or the answer that
// says why there is none.
func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, Answer) {
	id := c.nextID.Add(1)
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, Answer{Class: Protocol, Message: err.Error(), Err: err}
	}
	target := *c.base
	target.Path = c.base.Path + mcpPath

	status, header, body, ans, ok := c.send(ctx, http.MethodPost, &target, request)
	if !ok {
		return nil, ans
	}
	reply, ans := c.readReply(id, status, header, body)
	ans.Sent = true
	return reply, ans
}

// readReply classifies the HTTP status and the JSON-RPC envelope of an
// answer that arrived.
func (c *Client) readReply(id int64, status int, header http.Header, body []byte) (json.RawMessage, Answer) {
	if ans, ok := c.classifyStatus(status, header, body); !ok {
		return nil, ans
	}
	if status != http.StatusOK {
		return nil, Answer{Class: Protocol, Status: status, Message: string(body)}
	}

	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, Answer{Class: Protocol, Status: status, Message: "the answer is no JSON", Err: err}
	}
	if string(reply.ID) != strconv.FormatInt(id, 10) {
		return nil, Answer{Class: Protocol, Status: status, Message: "an answer to another request"}
	}
	if reply.Error != nil {
		class := Protocol
		if reply.Error.Code == rpcInvalidParams {
			class = InvalidInput
		}
		return nil, Answer{Class: class, Status: status, Message: reply.Error.Message}
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" {
		return nil, Answer{Class: Protocol, Status: status, Message: "an answer without result"}
	}
	return reply.Result, Answer{Class: Object, Status: status}
}

func jsonValid(b []byte) bool { return json.Valid(b) }
