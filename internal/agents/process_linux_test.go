package agents

import "testing"

func TestParseStat(t *testing.T) {
	tail := " S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 987654 20 21"
	for name, c := range map[string]struct {
		stat  string
		start int64
		alive bool
		ok    bool
	}{
		"plain":       {"42 (claude)" + tail, 987654, true, true},
		"odd name":    {"42 (a) b ) S (c)" + tail, 987654, true, true},
		"zombie":      {"42 (claude) Z" + tail[2:], 0, false, true},
		"no name":     {"42 claude" + tail, 0, false, false},
		"short":       {"42 (claude) S 1 2", 0, false, false},
		"not numeric": {"42 (claude) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 x 20", 0, false, false},
	} {
		start, alive, err := parseStat(c.stat)
		if start != c.start || alive != c.alive || (err == nil) != c.ok {
			t.Errorf("%s: %d %v %v", name, start, alive, err)
		}
	}
}
