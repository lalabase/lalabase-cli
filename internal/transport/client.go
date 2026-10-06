package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// DefaultTimeout bounds one request, as bin/lala did.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxBody bounds what is read of one answer. The largest answer the
	// server sends is a ticket with its capped comments and attachments, far
	// below this; a server sending more is broken or hostile.
	DefaultMaxBody = 8 << 20
	// defaultRetryPause is the wait after a 429 that names no window.
	defaultRetryPause = 5 * time.Second
	mcpPath           = "/api/mcp"
	apiPrefix         = "/api/"
)

// Client is one server and one token. It retries nothing; the request queue
// decides that from the answer's class.
type Client struct {
	base      *url.URL
	token     string
	http      *http.Client
	userAgent string
	maxBody   int64
	now       func() time.Time
	nextID    atomic.Int64
}

// Option changes a Client at construction.
type Option func(*Client)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithMaxBody sets the most bytes read of one answer.
func WithMaxBody(n int64) Option { return func(c *Client) { c.maxBody = n } }

// WithTimeout sets the time limit of one request.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.http.Timeout = d } }

// New checks the base address and the token and returns a client.
//
// The base address must use TLS, except for this machine (localhost,
// 127.0.0.1, ::1), where a development server runs: a token sent over plain
// HTTP to any other host can be read on the way. It may carry a path (a server
// under a sub path) but no user, query or fragment.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	base, err := checkBase(baseURL)
	if err != nil {
		return nil, err
	}
	if err := checkToken(token); err != nil {
		return nil, err
	}
	c := &Client{
		base:      base,
		token:     token,
		userAgent: "lalabase",
		maxBody:   DefaultMaxBody,
		now:       time.Now,
		http: &http.Client{
			Timeout: DefaultTimeout,
			// A redirect is never followed: the API does not redirect, and a
			// redirect to another host would carry the question there.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func checkBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("server address %q: %w", raw, err)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("server address %q has no host", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("server address %q may not carry a user, query or fragment", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("server address %q uses http: only https sends the token safely (http is allowed for localhost)", raw)
		}
	default:
		return nil, fmt.Errorf("server address %q: scheme must be https", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func checkToken(token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return errors.New("the token contains a space or a control character")
		}
	}
	return nil
}

// Get reads a REST endpoint under /api/ (e.g. "/api/v1/me" or
// "/api/v1/projects?organisation_id=...").
func (c *Client) Get(ctx context.Context, apiPath string) Answer {
	target, err := c.resolve(apiPath)
	if err != nil {
		return Answer{Class: Protocol, Message: err.Error(), Err: err}
	}
	status, header, body, ans, ok := c.send(ctx, http.MethodGet, target, nil)
	if !ok {
		return ans
	}
	return c.classifyREST(status, header, body)
}

// resolve joins an API path to the base address. Only a plain path under
// /api/ is accepted; anything that could leave the API (another host, a dot
// segment) is refused before a request is made.
func (c *Client) resolve(apiPath string) (*url.URL, error) {
	ref, err := url.Parse(apiPath)
	if err != nil {
		return nil, fmt.Errorf("path %q: %w", apiPath, err)
	}
	if ref.Scheme != "" || ref.Host != "" || ref.User != nil || ref.Fragment != "" {
		return nil, fmt.Errorf("path %q is no plain API path", apiPath)
	}
	if !strings.HasPrefix(ref.Path, apiPrefix) || path.Clean(ref.Path) != ref.Path || strings.Contains(apiPath, "//") {
		return nil, fmt.Errorf("path %q is not under %s", apiPath, apiPrefix)
	}
	u := *c.base
	u.Path = c.base.Path + ref.Path
	u.RawQuery = ref.RawQuery
	return &u, nil
}

// send performs one request. ok is false when no usable answer arrived; ans
// then holds the class (Temporary for the network, Protocol for the rest).
func (c *Client) send(ctx context.Context, method string, target *url.URL, body []byte) (status int, header http.Header, data []byte, ans Answer, ok bool) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return 0, nil, nil, Answer{Class: Protocol, Message: err.Error(), Err: err}, false
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, Answer{Class: Temporary, Message: "no answer from the server", Err: err}, false
	}
	defer func() { _ = resp.Body.Close() }()

	data, err = io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, Answer{Class: Temporary, Status: resp.StatusCode, Message: "the answer broke off", Err: err}, false
	}
	if int64(len(data)) > c.maxBody {
		err := fmt.Errorf("answer larger than %d bytes", c.maxBody)
		return resp.StatusCode, resp.Header, nil, Answer{Class: Protocol, Status: resp.StatusCode, Message: err.Error(), Err: err}, false
	}
	return resp.StatusCode, resp.Header, data, Answer{}, true
}

// classifyStatus places the HTTP statuses both surfaces share. ok is false
// for a status that decides the class on its own.
func (c *Client) classifyStatus(status int, header http.Header, body []byte) (Answer, bool) {
	switch {
	case status == http.StatusUnauthorized:
		return Answer{Class: Unauthenticated, Status: status, Message: string(body)}, false
	case status == http.StatusTooManyRequests:
		return Answer{Class: Temporary, Status: status, Message: string(body), RetryAt: c.retryAt(header)}, false
	case status >= 500:
		return Answer{Class: Temporary, Status: status, Message: string(body)}, false
	case status >= 300 && status < 400:
		return Answer{Class: Protocol, Status: status, Message: "the server redirected (" + header.Get("Location") + ")"}, false
	}
	return Answer{}, true
}

func (c *Client) classifyREST(status int, header http.Header, body []byte) Answer {
	if ans, ok := c.classifyStatus(status, header, body); !ok {
		return ans
	}
	switch status {
	case http.StatusOK:
		if !jsonValid(body) {
			return Answer{Class: Protocol, Status: status, Message: "the answer is no JSON"}
		}
		return Answer{Class: Object, Status: status, Payload: body}
	case http.StatusForbidden:
		return Answer{Class: NoAccess, Status: status, Message: string(body)}
	case http.StatusNotFound:
		return Answer{Class: NotFound, Status: status, Message: string(body)}
	case http.StatusUnprocessableEntity:
		return Answer{Class: InvalidInput, Status: status, Message: string(body)}
	}
	return Answer{Class: Protocol, Status: status, Message: string(body)}
}

// retryAt reads when a rate limit window opens again: Retry-After (seconds
// or an HTTP date) first, then RateLimit-Reset, which the core sends as an
// epoch and other servers as seconds. Without either, a short pause.
func (c *Client) retryAt(header http.Header) time.Time {
	now := c.now()
	at := now.Add(defaultRetryPause)
	if v := header.Get("Retry-After"); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			at = now.Add(time.Duration(s) * time.Second)
		} else if d, err := http.ParseTime(v); err == nil {
			at = d
		}
	} else if v := header.Get("RateLimit-Reset"); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			if s > 1e9 {
				at = time.Unix(s, 0).UTC()
			} else {
				at = now.Add(time.Duration(s) * time.Second)
			}
		}
	}
	if at.Before(now) {
		return now
	}
	return at
}
