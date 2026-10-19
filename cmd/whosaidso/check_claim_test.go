package main

// Admitting a claim in text mode says how to publish it and how to learn
// whether it won; nothing else prints that. Claim status is tested in
// internal/query and internal/acceptance.

import (
	"strings"
	"testing"
)

func TestCLIAdmittedClaimPrintsThePublishSteps(t *testing.T) {
	root, input := cliFixture(t)
	if _, errOut, code := cliRun(t, root, input, "agent", "capture", "--admit", "--reason", "task", "--events", "-"); code != 0 || strings.Contains(errOut, "claim") {
		t.Fatalf("control: a task admission prints no claim note: %d %q", code, errOut)
	}
	out, errOut, code := cliRun(t, root, nil, "agent", "template", "task.start", "--task", string(cliID(1)), "--capture", "--admit", "--reason", "mine")
	if code != 0 || !strings.Contains(out, "admitted") {
		t.Fatalf("control: the claim admits: %d %q %q", code, out, errOut)
	}
	for _, want := range []string{"git push", "git fetch origin && git merge --no-edit origin/main", "whosaidso check claim "} {
		if !strings.Contains(errOut, want) {
			t.Errorf("admitting a claim must say %q on stderr, got %q", want, errOut)
		}
	}
}
