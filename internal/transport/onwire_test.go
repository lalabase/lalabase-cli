package transport

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Review #9, round 3: a callback net/http runs on a goroutine of its own
// cannot panic there, where no supervisor recovers it. Each wrapper keeps
// the panic, and raise throws it again on the request's goroutine, with a
// fixed value: the callback's own value is foreign and never passed on.
func TestACallbackPanicIsRaisedOnTheRequestsGoroutine(t *testing.T) {
	const secret = "secret lalab_pat_x"
	for name, hook := range map[string]func(*onWire) func(){
		"no argument":  func(w *onWire) func() { return onWire0(w, func() { panic(secret) }) },
		"one argument": func(w *onWire) func() { h := onWire1(w, func(string) { panic(secret) }); return func() { h("x") } },
		"two arguments": func(w *onWire) func() {
			h := onWire2(w, func(int, error) { panic(secret) })
			return func() { h(1, nil) }
		},
	} {
		var w onWire
		call := hook(&w)
		done := make(chan any, 1)
		go func() { // net/http's goroutine: a panic here would end the test binary
			defer func() { done <- recover() }()
			call()
		}()
		if escaped := <-done; escaped != nil {
			t.Fatalf("%s: the panic left the wrapper: %v", name, escaped)
		}
		raised := func() (v any) {
			defer func() { v = recover() }()
			w.raise()
			return nil
		}()
		if s, ok := raised.(string); !ok || s != callbackPanicked || strings.Contains(s, "secret") {
			t.Fatalf("%s: raised %#v", name, raised)
		}
	}
	var calm onWire
	onWire0(&calm, func() {})()
	onWire1(&calm, func(int) {})(1)
	onWire2(&calm, func(int, int) {})(1, 2)
	calm.raise() // nothing to raise
}

// tracked is an answer body that records being read and closed.
type tracked struct {
	read, closed bool
	body         *strings.Reader
}

func (b *tracked) Read(p []byte) (int, error) { b.read = true; return b.body.Read(p) }
func (b *tracked) Close() error               { b.closed = true; return nil }

type answerWith struct{ body *tracked }

func (a answerWith) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: a.body}, nil
}

// Review #9, round 4: send raises a kept hook panic itself, with the fixed
// value, once right after Do (before the body is read) and once when it
// returns (a hook that ran late); the body is closed either way.
func TestSendRaisesAKeptHookPanic(t *testing.T) {
	for _, step := range []string{"afterDo", "afterBody", ""} {
		body := &tracked{body: strings.NewReader(`{"ok":true}`)}
		c, err := New("https://lalabase.example", "lalab_pat_synthetic", WithTransport(answerWith{body}))
		if err != nil {
			t.Fatal(err)
		}
		c.wireStep = func(at string, w *onWire) {
			if at == step {
				onWire0(w, func() { panic("secret lalab_pat_x") })()
			}
		}
		target, _ := url.Parse("https://lalabase.example/api/v1/me")
		raised := func() (v any) {
			defer func() { v = recover() }()
			c.send(context.Background(), http.MethodGet, target, nil)
			return nil
		}()
		switch {
		case step == "" && raised != nil:
			t.Fatalf("no hook panicked, raised %#v", raised)
		case step != "" && raised != callbackPanicked:
			t.Fatalf("%s: raised %#v", step, raised)
		case step == "afterDo" && body.read:
			t.Fatal("afterDo: the body was read before the panic was raised")
		case !body.closed:
			t.Fatalf("%s: the body was left open", step)
		}
	}
}
