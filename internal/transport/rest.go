package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// keyForm is what an idempotency key of this client looks like. The server
// strips a key and treats a blank one as none, writing without remembering
// it, and refuses one longer than 255 bytes; a key of this form survives
// both unchanged, and is safe in a header line.
var keyForm = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

// ValidKey says whether key is an idempotency key this client may send.
// It is the one rule for MCP and REST writes alike.
func ValidKey(key string) bool { return keyForm.MatchString(key) }

// restCodeClasses maps the machine field of a REST error (the core's R0,
// "code") to a class: the codes of the tools, and the 401's own. A code not
// listed here is a refusal.
var restCodeClasses = func() map[string]Class {
	m := map[string]Class{"unauthorized": Unauthenticated}
	for code, class := range codeClasses {
		m[code] = class
	}
	return m
}()

// restError is the API's error body: {"error": "...", "code": "...",
// "reason": "..."}, code and reason since R0. Code is empty unless the body
// carries both code and error as strings.
type restError struct {
	Error  string
	Code   string
	Reason string
}

// readRESTError reads the API's error body. "error" is read on its own, as
// before R0, so a body whose other fields are of another type is placed by
// it as it was; code and reason are read each on its own too. They count
// only beside a non-empty "error": a
// proxy's body that happens to carry a "code" is not the API's answer.
func readRESTError(body []byte) restError {
	var e restError
	var old struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &old) != nil || old.Error == "" {
		return e
	}
	e.Error = old.Error
	// Each field on its own: a reason of another type does not take the
	// code with it.
	var code struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &code) == nil {
		e.Code = code.Code
	}
	var reason struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &reason) == nil {
		e.Reason = reason.Reason
	}
	return e
}

// RefusalText is what a REST error says to a person: the first of its
// "errors" that says anything (one localized message, since the core's R6),
// else its "message"; empty when it names neither, as the API's 403 and 404
// do, or when the answer is not in the API's own form. It is for showing
// alone: what the client does follows from the class. Raw like Message,
// whose body it reads.
func (a Answer) RefusalText() string {
	body := []byte(a.Message)
	if readRESTError(body).Error == "" {
		return ""
	}
	// Each field on its own, as in readRESTError: one of another type does
	// not take the other with it.
	var list struct {
		Errors []any `json:"errors"`
	}
	if json.Unmarshal(body, &list) == nil {
		for _, item := range list.Errors {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	var one struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &one) == nil && strings.TrimSpace(one.Message) != "" {
		return one.Message
	}
	return ""
}

// codeStatus names the status the codes that drop a record from the cache
// come with. Beside another status such a code contradicts itself, and is
// a refusal: nothing a server says unclearly drops a cached entry.
var codeStatus = map[string]int{
	"not_found": http.StatusNotFound,
	"forbidden": http.StatusForbidden,
}

// Write sends one REST write: POST or PATCH under /api/, with body as its
// JSON object and key as its Idempotency-Key. The caller encodes the body
// once and sends the same bytes on every retry of the person's action: the
// server answers a repeat under the same key with its first answer, and a
// different request under it as an invalid input.
//
// A write without a usable key, with another method or with a body that is
// no JSON object is refused before anything is sent (InvalidInput, not
// sent). A success is 200 or 201.
func (c *Client) Write(ctx context.Context, method, apiPath string, body json.RawMessage, key string) Answer {
	if err := checkWrite(method, body, key); err != nil {
		return Answer{Class: InvalidInput, Message: err.Error(), Err: err}
	}
	target, err := c.resolve(apiPath)
	if err != nil {
		return Answer{Class: Protocol, Message: err.Error(), Err: err}
	}
	status, header, data, ans, ok := c.sendWith(ctx, method, target, body, http.Header{"Idempotency-Key": {key}})
	if !ok {
		return ans
	}
	ans = c.classifyREST(status, header, data, true)
	ans.Sent = true
	return ans
}

func checkWrite(method string, body json.RawMessage, key string) error {
	switch {
	case method != http.MethodPost && method != http.MethodPatch:
		return errors.New("a write is a POST or a PATCH, not " + method)
	case !ValidKey(key):
		return errors.New("a write needs an idempotency key of 8 to 128 letters, digits or ._:-")
	case !json.Valid(body) || !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")):
		return errors.New("the body of a write is a JSON object")
	}
	return nil
}
