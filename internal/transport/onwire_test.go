package transport

import (
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
