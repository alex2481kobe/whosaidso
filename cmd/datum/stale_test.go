package main

// `datum show --stale` (R14.2; R19: formerly state --stale) through a fresh
// process: the stale-claims section appears only when asked, git runs only
// when asked, and the flag belongs to show alone. What staleness is (TRUE, FALSE, UNKNOWN against a
// real repository) is tested in internal/write.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIShowStaleRunsGitOnlyWhenAsked(t *testing.T) {
	root, _, records := disposalWorld(t)
	claim := string(records[0].RecordID)
	// A git that records every call and answers nothing. The fixture is not a
	// checkout, so staleness must come back UNKNOWN with a reason.
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "git-calls")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	read := func(args ...string) ([]byte, error) {
		command := exec.Command(binary, append([]string{"-test.run=^TestDatumMainProcess$", "--"}, args...)...)
		command.Dir = root
		command.Env = append(os.Environ(), "DATUM_MAIN_TEST_PROCESS=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		if err != nil {
			return stderr.Bytes(), err
		}
		return stdout.Bytes(), nil
	}
	plain, err := read("show", "--json")
	if err != nil {
		t.Fatalf("control: show must answer: %v %s", err, plain)
	}
	if bytes.Contains(plain, []byte(`"stale"`)) {
		t.Fatal("show without --stale must carry no stale section")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("show without --stale ran git; reads stay cheap unless asked")
	}
	out, err := read("show", "--stale", "--json")
	if err != nil {
		t.Fatalf("show --stale: %v %s", err, out)
	}
	var answer struct {
		Stale struct {
			BlindSpot string `json:"blind_spot"`
			Claims    []struct {
				Claim struct {
					RecordID string `json:"record_id"`
				} `json:"claim"`
				Stale  string `json:"stale"`
				Reason string `json:"reason"`
			} `json:"claims"`
		} `json:"stale"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	stale := answer.Stale.Claims
	if len(stale) != 1 || stale[0].Claim.RecordID != claim || stale[0].Stale != "UNKNOWN" || stale[0].Reason == "" {
		t.Fatalf("the observed claim must read stale UNKNOWN with git's reason: %s", out)
	}
	if calls, err := os.ReadFile(log); err != nil || len(calls) == 0 || !strings.Contains(answer.Stale.BlindSpot, "uncommitted changes") {
		t.Fatalf("show --stale must ask git and state its blind spot: %v %q", err, answer.Stale.BlindSpot)
	}
	brief, err := read("show", "--stale")
	if err != nil || !strings.Contains(string(brief), "stale claims: 1\n  CLAIM "+claim+" rev 1 stale UNKNOWN") {
		t.Fatalf("the brief must list the stale section: %v\n%s", err, brief)
	}
	if out, err := read("todo", "--stale"); err == nil {
		t.Fatalf("--stale belongs to show alone, todo accepted it: %s", out)
	}
}
