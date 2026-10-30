package ui

import (
	"testing"
	"time"
)

// A server time is shown in the person's zone, without seconds, with the
// year only when it is not the current one (in that zone).
func TestTimesAreLocalShortAndYearOnlyWhenOther(t *testing.T) {
	t.Parallel()

	berlin := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw  string
		loc  *time.Location
		now  time.Time
		want string
	}{
		{"2026-08-28T15:45:03Z", time.UTC, now, "28 Aug 15:45"},
		{"2026-08-28T15:45:03Z", berlin, now, "28 Aug 17:45"},
		{"2026-08-28T15:45:03.123+02:00", time.UTC, now, "28 Aug 13:45"},
		{"2025-11-03T09:12:00Z", time.UTC, now, "3 Nov 2025 09:12"},
		// New year's eve in UTC is already the new year in Berlin.
		{"2025-12-31T23:30:00Z", berlin, time.Date(2026, 1, 1, 0, 0, 0, 0, berlin), "1 Jan 01:30"},
		{"2025-12-31T23:30:00Z", time.UTC, time.Date(2026, 1, 1, 0, 0, 0, 0, berlin), "31 Dec 23:30"},
		{"not a time", time.UTC, now, "Time unknown"},
	} {
		if got := timeOf(tc.raw, tc.now, tc.loc); got != tc.want {
			t.Errorf("%s in %s: %q, want %q", tc.raw, tc.loc, got, tc.want)
		}
	}
}
