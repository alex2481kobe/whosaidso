package main

// `datum state --stale` (R14.2) through a fresh process: the stale-claims
// section appears only when asked, git runs only when asked, and the flag
// belongs to state alone. What staleness is (TRUE, FALSE, UNKNOWN against a
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

func TestCLIStateStaleRunsGitOnlyWhenAsked(t *testing.T) {
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
	plain, err := read("state", "--json")
	if err != nil {
		t.Fatalf("control: state must answer: %v %s", err, plain)
	}
	if bytes.Contains(plain, []byte(`"stale"`)) {
		t.Fatal("state without --stale must carry no stale section")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("state without --stale ran git; reads stay cheap unless asked")
	}
	out, err := read("state", "--stale", "--json")
	if err != nil {
		t.Fatalf("state --stale: %v %s", err, out)
	}
	var answer struct {
		Preset struct {
			Stale []struct {
				Claim struct {
					RecordID string `json:"record_id"`
				} `json:"claim"`
				Stale  string `json:"stale"`
				Reason string `json:"reason"`
			} `json:"stale"`
		} `json:"preset"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	stale := answer.Preset.Stale
	if len(stale) != 1 || stale[0].Claim.RecordID != claim || stale[0].Stale != "UNKNOWN" || stale[0].Reason == "" {
		t.Fatalf("the observed claim must read stale UNKNOWN with git's reason: %s", out)
	}
	if calls, err := os.ReadFile(log); err != nil || len(calls) == 0 {
		t.Fatalf("state --stale must ask git: %v", err)
	}
	brief, err := read("state", "--stale")
	if err != nil || !strings.Contains(string(brief), "stale claims: 1\n  CLAIM "+claim+" rev 1 stale UNKNOWN") {
		t.Fatalf("the brief must list the stale section: %v\n%s", err, brief)
	}
	if out, err := read("now", "--stale"); err == nil {
		t.Fatalf("--stale belongs to state alone, now accepted it: %s", out)
	}
}
