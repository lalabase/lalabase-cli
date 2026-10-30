package sourceguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A test lives in the file of its topic, where the next reader looks for
// it. A file named after a review round tells no one what it holds: thirty
// five of them had grown before they were renamed.
var reviewFileName = regexp.MustCompile(`(^|_)(review|round)[0-9]*[a-z]?(_|$)`)

func TestNoTestFileIsNamedAfterAReview(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "git", "ls-files", "../..").Output()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skip("no git here")
	}
	for _, path := range strings.Fields(string(out)) {
		name, isTest := strings.CutSuffix(filepath.Base(path), "_test.go")
		if isTest && reviewFileName.MatchString(name) {
			t.Errorf("%s: name the file after its topic, not after a review", path)
		}
	}
}

func TestTheReviewFileNameIsRecognised(t *testing.T) {
	for name, want := range map[string]bool{
		"review1_auth": true, "agents_review6": true, "round2": true, "review3a_probe": true,
		"preview": false, "focus_ground": false, "background": false,
	} {
		if got := reviewFileName.MatchString(name); got != want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}
