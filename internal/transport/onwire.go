package transport

import "sync/atomic"

// callbackPanicked is the fixed value a callback's panic is raised again
// with: the callback's own value is foreign and may carry anything.
const callbackPanicked = "transport: a callback of net/http panicked"

// onWire carries the panics of the callbacks net/http runs on goroutines of
// its own (the httptrace hooks). No supervisor recovers a panic there: it
// would end the process with its value printed and the terminal left as it
// was. A wrapped callback keeps the panic instead, and raise throws it again
// on the request's goroutine, where the caller's recovery applies. The
// source guard requires these wrappers for such callbacks.
type onWire struct{ panicked atomic.Bool }

func (w *onWire) catch() {
	if recover() != nil {
		w.panicked.Store(true)
	}
}

// raise throws a kept panic on the caller's goroutine.
func (w *onWire) raise() {
	if w.panicked.Load() {
		panic(callbackPanicked)
	}
}

func onWire0(w *onWire, f func()) func() {
	return func() { defer w.catch(); f() }
}

func onWire1[A any](w *onWire, f func(A)) func(A) {
	return func(a A) { defer w.catch(); f(a) }
}

func onWire2[A, B any](w *onWire, f func(A, B)) func(A, B) {
	return func(a A, b B) { defer w.catch(); f(a, b) }
}
