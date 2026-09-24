package main

// What writes print (R19 step 2): one-line acknowledgements by default, the
// full result with --json, minted admission ids, and capture --admit's
// honest partial success when its second act is refused.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// cliRun runs one command in process and returns stdout, stderr and the exit status.
func cliRun(t *testing.T, root string, input []byte, actor string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := whosaidso(context.Background(), args, root, bytes.NewReader(input), &stdout, &stderr, func(key string) string {
		if key == "WHOSAIDSO_ACTOR" {
			return actor
		}
		return ""
	})
	return stdout.String(), stderr.String(), code
}

var ulid = `[0-9A-HJKMNP-TV-Z]{26}`

func TestWritesAcknowledgeCompactlyAndMintAdmissionIDs(t *testing.T) {
	root, data := cliFixture(t)
	out, errs, code := cliRun(t, root, data, "agent", "capture", "--command-id", string(cliID(3)))
	if code != 0 || out != "captured "+string(cliID(3))+" (1 events) command "+string(cliID(3))+"\n" {
		t.Fatalf("capture must acknowledge in one line: %d %q %q", code, out, errs)
	}
	// No --command-id: admit mints one and names it in the bundle it prints.
	out, errs, code = cliRun(t, root, nil, "agent", "admit", "--outcome", "accepted", "--reason", "checked", string(cliID(3)))
	if code != 0 || !regexp.MustCompile(`^admitted `+string(cliID(3))+` bundle 00000001-`+ulid+" self-admitted\n$").MatchString(out) {
		t.Fatalf("admit must mint its id and acknowledge in one line: %d %q %q", code, out, errs)
	}
	minted := model.ID(strings.Fields(out)[3][9:])
	// An explicit id is a deterministic retry: the same bundle comes back.
	again, _, code := cliRun(t, root, nil, "agent", "admit", "--command-id", string(minted), "--outcome", "accepted", "--reason", "checked", string(cliID(3)))
	if code != 0 || again != out {
		t.Fatalf("a retry under the minted id must return the same bundle: %d %q, want %q", code, again, out)
	}
	full, _, code := cliRun(t, root, nil, "agent", "admit", "--json", "--command-id", string(minted), "--outcome", "accepted", "--reason", "checked", string(cliID(3)))
	var bundle model.Bundle
	if code != 0 || json.Unmarshal([]byte(full), &bundle) != nil || bundle.CommandID != minted || bundle.Sequence != 1 {
		t.Fatalf("--json must print the full bundle: %d %s", code, full)
	}
}

func TestCaptureAdmitReportsBothActs(t *testing.T) {
	root, data := cliFixture(t)
	out, errs, code := cliRun(t, root, data, "agent", "capture", "--admit", "--reason", "one step")
	lines := strings.Split(out, "\n")
	if code != 0 || len(lines) != 3 || !regexp.MustCompile(`^captured `+ulid+` \(1 events\) command `+ulid+`$`).MatchString(lines[0]) ||
		!regexp.MustCompile(`^admitted `+ulid+` bundle 00000001-`+ulid+` self-admitted$`).MatchString(lines[1]) {
		t.Fatalf("capture --admit must mint both ids and acknowledge both acts: %d %q %q", code, out, errs)
	}
	// A source carries no record author, so an unknown actor can capture and admit it.
	root, _ = cliFixture(t)
	body := []byte("the owner's words")
	blob := filepath.Join(root, "words.txt")
	if err := os.WriteFile(blob, body, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := model.EncodeEvent(&model.SourceIntake{SourceID: cliID(20), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{},
		SourceRef: model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = model.Encode([]model.Event{source})
	out, _, code = cliRun(t, root, data, "", "capture", "--json", "--blob", blob, "--admit", "--reason", "one step", "--admit-command-id", string(cliID(9)))
	var result map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &result) != nil {
		t.Fatalf("capture --admit --json: %d %s", code, out)
	}
	admission := result["admission"].(map[string]any)
	if result["capture"].(map[string]any)["status"] != "captured" || admission["status"] != "admitted" || admission["command_id"] != string(cliID(9)) ||
		result["pending"] != false || result["self_admitted"] != "UNKNOWN" || admission["refusal"] != nil {
		t.Fatalf("an unknown author and unknown admitter must never read as self-admitted, and the explicit id is kept: %s", out)
	}
}

// Admission refused after a successful capture is a partial success, never
// a full one and never a plain failure: exit 4, the capture reported as
// captured, the packet pending in intake, and the command to retry it.
func TestCaptureAdmitRefusalIsAPartialSuccess(t *testing.T) {
	root, _ := cliFixture(t)
	start, err := model.EncodeEvent(&model.TaskStart{Task: model.RecordRef{Project: "test/cli", RecordID: cliID(1), Revision: 1}, Actor: model.Actor{ID: "agent"}, AttemptID: cliID(7)})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := model.Encode([]model.Event{start}) // starts a task nobody admitted
	project, _ := store.Discover(root)
	out, errs, code := cliRun(t, root, data, "agent", "capture", "--json", "--command-id", string(cliID(5)), "--admit", "--admit-command-id", string(cliID(6)), "--reason", "try")
	var result struct {
		Capture   struct{ Status, CommandID string } `json:"capture"`
		Admission struct {
			Status  string  `json:"status"`
			Bundle  *string `json:"bundle"`
			Refusal *string `json:"refusal"`
		} `json:"admission"`
		Pending bool   `json:"pending"`
		Retry   string `json:"retry"`
	}
	if code != 4 || json.Unmarshal([]byte(out), &result) != nil || result.Capture.Status != "captured" || result.Admission.Status != "refused" ||
		result.Admission.Bundle != nil || result.Admission.Refusal == nil || !result.Pending ||
		result.Retry != "whosaidso admit --command-id "+string(cliID(6))+" --actor 'agent' --outcome accepted --reason 'try' "+string(cliID(5)) || !strings.Contains(errs, "partial success") {
		t.Fatalf("a refused admission after a capture must exit 4 with the partial result: %d %s %s", code, out, errs)
	}
	if prefix, err := store.ReadPrefix(project); err != nil || len(prefix) != 0 {
		t.Fatalf("the refused admission published: %v %v", prefix, err)
	}
	if packets, err := store.ReadIntake(project, []model.ID{cliID(5)}); err != nil || len(packets) != 1 {
		t.Fatalf("the captured packet must stay in intake, pending: %v", err)
	}
	out, _, code = cliRun(t, root, data, "agent", "capture", "--command-id", string(cliID(15)), "--admit", "--admit-command-id", string(cliID(16)), "--reason", "try")
	if code != 4 || !strings.HasPrefix(out, "captured "+string(cliID(15))+"; admission "+string(cliID(16))+" refused: ") || !strings.Contains(out, "; packet stays pending\nretry: whosaidso admit ") {
		t.Fatalf("the text must say the capture stands and the packet is pending: %d %q", code, out)
	}
}

func TestCaptureAdmitFlagsAreUsageErrors(t *testing.T) {
	root, data := cliFixture(t)
	for _, args := range [][]string{
		{"capture", "--admit"},
		{"capture", "--admit", "--reason", " "},
		{"capture", "--reason", "orphan"},
		{"capture", "--admit-command-id", string(cliID(9))},
	} {
		if out, _, code := cliRun(t, root, data, "agent", args...); code != 2 || out != "" {
			t.Fatalf("%v must be a usage error (exit 2) with no answer, got %d %q", args, code, out)
		}
	}
	project, _ := store.Discover(root)
	if ids, err := store.IntakeIDs(project); err != nil || len(ids) != 0 {
		t.Fatalf("a usage error captured: %v %v", ids, err)
	}
}

// run --admit admits the start and seal it captured, as capture --admit does.
func TestRunAdmitAdmitsItsStartAndSeal(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	out, errs, code := cliRun(t, root, nil, "agent", "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1",
		"--admit", "--reason", "measured", "--", "/bin/sh", "tools/measure.sh")
	lines := strings.Split(out, "\n")
	if code != 0 || len(lines) != 3 || !regexp.MustCompile(`^run `+ulid+` exit 0; start (`+ulid+`) seal (`+ulid+`); admit: whosaidso admit --outcome accepted --reason REASON `+ulid+` `+ulid+`$`).MatchString(lines[0]) ||
		!regexp.MustCompile(`^admitted `+ulid+` `+ulid+` bundle [0-9]{8}-`+ulid+` self-admitted$`).MatchString(lines[1]) {
		t.Fatalf("run --admit must acknowledge the run and its admission: %d %q %q", code, out, errs)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != "MEASURED" {
		t.Fatalf("the admitted run must measure the claim, got %s", status)
	}
	if _, _, code := cliRun(t, root, nil, "agent", "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID), "--admit", "--", "/bin/true"); code != 2 {
		t.Fatalf("run --admit without --reason must be a usage error, got %d", code)
	}
}
