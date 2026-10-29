package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
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
// "reason": "..."}, code and reason since R0.
type restError struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// readRESTError reads the API's error body. ok is false for anything else:
// no JSON object, a field of another type, or no "error" (a proxy's body
// that happens to carry a "code" is not the API's answer).
func readRESTError(body []byte) (restError, bool) {
	var e restError
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		return restError{}, false
	}
	return e, true
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
