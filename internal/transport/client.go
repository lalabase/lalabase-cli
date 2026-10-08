package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
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
	// maxRetryPause bounds the wait a 429 can ask for. The core's windows are
	// at most a minute; a server asking for days is broken or hostile, and the
	// whole background work would wait with it.
	maxRetryPause = 5 * time.Minute
	mcpPath       = "/api/mcp"
	apiPrefix     = "/api/"
)

// Client is one server and one token. It retries nothing; the request queue
// decides that from the answer's class.
type Client struct {
	base      *url.URL
	token     string
	http      *http.Client
	version   string
	userAgent string
	maxBody   int64
	now       func() time.Time
	nextID    atomic.Int64
	// wireStep lets a test make a hook panic at a step of send ("afterDo",
	// "afterBody"); nil in the program.
	wireStep func(step string, w *onWire)
}

// Option changes a Client at construction.
type Option func(*Client)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

// WithVersion names the client's version: in the User-Agent header
// ("lalabase/<version>") and in initialize's clientInfo.
func WithVersion(v string) Option {
	return func(c *Client) { c.version, c.userAgent = v, "lalabase/"+v }
}

// WithTransport replaces the HTTP transport (tests: a server's own
// certificate).
func WithTransport(rt http.RoundTripper) Option { return func(c *Client) { c.http.Transport = rt } }

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
		version:   "dev",
		userAgent: "lalabase/dev",
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
	if err := checkHeaderValue(c.version); err != nil {
		return nil, fmt.Errorf("version: %w", err)
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
	if err := checkHeaderValue(token); err != nil {
		return fmt.Errorf("token: %w", err)
	}
	return nil
}

// checkHeaderValue allows printable ASCII without spaces: what goes into a
// header line can then not end it.
func checkHeaderValue(v string) error {
	if v == "" {
		return errors.New("empty")
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return errors.New("contains a space or a control character")
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
	ans = c.classifyREST(status, header, body)
	ans.Sent = true
	return ans
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

	// Sent from the moment the headers are written: with HTTP/2 a canceled
	// request can end before WroteRequest, while the server already reads it.
	// Counting too early is the safe side; a write is then reloaded, not
	// sent again. handshake: a TLS connection was agreed, freshly or on a
	// reused connection; after that, a TLS error is the network, not the setup.
	// The hooks run on net/http's goroutines: wrapped, so a panic in them
	// is raised here, after Do (and again when send returns, for a hook that
	// ran late).
	var sent, handshake atomic.Bool
	var wire onWire
	defer wire.raise()
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: onWire0(&wire, func() { sent.Store(true) }),
		WroteRequest: onWire1(&wire, func(httptrace.WroteRequestInfo) { sent.Store(true) }),
		TLSHandshakeDone: onWire2(&wire, func(_ tls.ConnectionState, err error) {
			if err == nil {
				handshake.Store(true)
			}
		}),
		GotConn: onWire1(&wire, func(info httptrace.GotConnInfo) {
			if info.Reused {
				handshake.Store(true)
			}
		}),
	}))

	resp, err := c.http.Do(req)
	if err == nil {
		// Registered before any raise: a raised panic closes the body too.
		defer func() { _ = resp.Body.Close() }()
	}
	c.step("afterDo", &wire)
	wire.raise()
	if err != nil {
		return 0, nil, nil, failure(ctx, err, sent.Load(), handshake.Load(), "no answer from the server"), false
	}

	data, err = io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	c.step("afterBody", &wire)
	if err != nil {
		ans := failure(ctx, err, true, true, "the answer broke off")
		ans.Status = resp.StatusCode
		return resp.StatusCode, resp.Header, nil, c.window(ans, resp.Header), false
	}
	if int64(len(data)) > c.maxBody {
		err := fmt.Errorf("answer larger than %d bytes", c.maxBody)
		ans := Answer{Class: Protocol, Status: resp.StatusCode, Message: err.Error(), Err: err, Sent: true}
		return resp.StatusCode, resp.Header, nil, c.window(ans, resp.Header), false
	}
	return resp.StatusCode, resp.Header, data, Answer{}, true
}

// step runs a test's hook step, if any.
func (c *Client) step(at string, w *onWire) {
	if c.wireStep != nil {
		c.wireStep(at, w)
	}
}

// window gives an answer whose headers said 429 the server's window, whatever
// became of the rest of it (a body that broke off or was too large, a caller
// that left): the window is closed all the same, and the queue pauses on it.
func (c *Client) window(ans Answer, header http.Header) Answer {
	if ans.Status == http.StatusTooManyRequests {
		ans.RetryAt = c.retryAt(header)
	}
	return ans
}

// failure classifies a request that brought no usable answer. The client's
// own cancellation is no failure of the server; a certificate that does not
// check out, or a plain HTTP server behind an https address, is no network
// hiccup to retry but a broken or attacked setup; the rest is the network.
func failure(ctx context.Context, err error, sent, handshakeDone bool, msg string) Answer {
	var (
		verify    *tls.CertificateVerificationError
		authority x509.UnknownAuthorityError
		hostname  x509.HostnameError
		invalid   x509.CertificateInvalidError
		header    tls.RecordHeaderError
		netErr    net.Error
	)
	timeout := errors.As(err, &netErr) && netErr.Timeout()
	switch {
	case errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled):
		return Answer{Class: Canceled, Message: "canceled", Err: err, Sent: sent}
	case errors.As(err, &verify), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &invalid):
		return Answer{Class: Protocol, Message: "the server's certificate does not check out: " + err.Error(), Err: err, Sent: sent}
	case errors.As(err, &header), strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return Answer{Class: Protocol, Message: "the server answers plain HTTP at an https address", Err: err, Sent: sent}
	// A handshake the two sides cannot agree on (protocol version, cipher,
	// an alert from the server) stays so on every retry. Over TCP Go reports
	// these only as text ("remote error: tls: ...", "tls: server selected
	// ..."; its AlertError type is for QUIC), so the text decides, and only
	// before a handshake succeeded: a TLS error after it (a broken record in
	// the answer's headers or body, which http.Client.Do reads too) is the
	// network and may be retried.
	case !handshakeDone && !timeout && strings.Contains(err.Error(), "tls: "):
		return Answer{Class: Protocol, Message: "no TLS connection could be agreed with the server: " + err.Error(), Err: err, Sent: sent}
	case strings.Contains(err.Error(), "invalid header field"):
		return Answer{Class: Protocol, Message: "the request could not be built: " + err.Error(), Err: err, Sent: sent}
	}
	return Answer{Class: Temporary, Message: msg, Err: err, Sent: sent}
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
	// A 403 or 404 counts only in the API's own form: one from a proxy, a
	// firewall or a wrong base address says nothing about the record and
	// must not make the cache drop it.
	case http.StatusForbidden:
		if apiError(body) == "forbidden" {
			return Answer{Class: NoAccess, Status: status, Message: string(body)}
		}
	case http.StatusNotFound:
		if apiError(body) == "not_found" {
			return Answer{Class: NotFound, Status: status, Message: string(body)}
		}
	case http.StatusUnprocessableEntity:
		return Answer{Class: InvalidInput, Status: status, Message: string(body)}
	}
	return Answer{Class: Protocol, Status: status, Message: string(body)}
}

// apiError reads the "error" of the API's error body ({"error":"..."}).
func apiError(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Error
}

// retryAt reads when a rate limit window opens again: Retry-After (seconds
// or an HTTP date) first, then RateLimit-Reset, which the core sends as an
// epoch and other servers as seconds. Without either, a short pause.
func (c *Client) retryAt(header http.Header) time.Time {
	now := c.now()
	at := now.Add(defaultRetryPause)
	// Seconds are bounded before they become a duration: a value near the
	// int64 limit would overflow into the past and mean "no pause".
	seconds := func(s int64) time.Time {
		limit := int64(maxRetryPause / time.Second)
		return now.Add(time.Duration(min(max(s, 0), limit)) * time.Second)
	}
	if v := header.Get("Retry-After"); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			at = seconds(s)
		} else if d, err := http.ParseTime(v); err == nil {
			at = d
		}
	} else if v := header.Get("RateLimit-Reset"); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			if s > 1e9 && s < 1e11 {
				at = time.Unix(s, 0).UTC()
			} else {
				at = seconds(s)
			}
		}
	}
	if at.Before(now) {
		return now
	}
	if limit := now.Add(maxRetryPause); at.After(limit) {
		return limit
	}
	return at
}
