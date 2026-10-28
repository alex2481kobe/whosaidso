// Large synthetic ledgers measure process startup and owner screens. This file
// never uses a checkout's ledger or treats a prebuilt cache as an uncached load.
package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestLargeLedgerCommands(t *testing.T) {
	if os.Getenv("WHOSAIDSO_PERF") != "1" {
		t.Skip("set WHOSAIDSO_PERF=1 for large synthetic measurements")
	}
	host, session, p := largeLedger(t)
	bin := filepath.Join(t.TempDir(), "whosaidso")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/whosaidso")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	start := time.Now()
	code, _, body := largeScreen(t, host, session)
	if code != 200 {
		t.Fatalf("first screen %d: %s", code, body)
	}
	measureLarge(t, "first Overview, no index", time.Since(start), time.Second)
	start = time.Now()
	code, _, body = largeScreen(t, host, session)
	if code != 200 {
		t.Fatalf("warm screen %d: %s", code, body)
	}
	measureLarge(t, "Overview warm", time.Since(start), 200*time.Millisecond)
	start = time.Now()
	code, _, body = ask(t, host, "GET", "/api/screen?project=team%2Fok&screen=status", "", session)
	if code != 200 {
		t.Fatalf("status: %d %s", code, body)
	}
	measureLarge(t, "status warm", time.Since(start), time.Second)
	// A fresh Reader stands for a new viewer process using its persistent index.
	start = time.Now()
	var reader store.Reader
	source, err := reader.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	measureLarge(t, "fresh reader with index", time.Since(start), time.Second)
	start = time.Now()
	todo, err := query.ReadViewFrom(p, query.ViewRequest{View: "todo"}, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("todo selection only: %s", time.Since(start))
	start = time.Now()
	if err := query.RenderViewBrief(io.Discard, todo); err != nil {
		t.Fatal(err)
	}
	t.Logf("todo text only: %s", time.Since(start))
	// The CLI is a different build from this test executable and deliberately
	// cannot reuse its image. Prepare the CLI's own index before timing restarts.
	prepare := exec.Command(bin, "todo", "--limit", "1")
	prepare.Dir = p.Root
	if out, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare CLI: %v %s", err, out)
	}
	for _, args := range [][]string{{"todo"}, {"todo", "--top"}, {"todo", "--under", string(largeID(1))}, {"check", "claim", string(largeID(1)), "--json"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = p.Root
		start = time.Now()
		out, err := cmd.Output()
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if args[0] != "check" || !ok || exit.ExitCode() != 3 || !bytes.Contains(out, []byte("UNKNOWN")) {
				t.Fatalf("%v: %v %s", args, err, out)
			}
		}
		measureLarge(t, "fresh process "+strings.Join(args, " ")+" with index", time.Since(start), time.Second)
	}

	largeHeldClaim(t, p, source, bin)

}

func measureLarge(t *testing.T, label string, elapsed, limit time.Duration) {
	t.Helper()
	t.Logf("%s: %s (target %s)", label, elapsed, limit)
	if os.Getenv("WHOSAIDSO_PERF_CHECK") == "1" && elapsed >= limit {
		t.Errorf("%s exceeds %s: %s", label, limit, elapsed)
	}
}

func largeScreen(t *testing.T, host string, session sess) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", "http://"+host+"/api/screen?project=team%2Fok&screen=home", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", session.cookie)
	req.Header.Set("X-WhoSaidSo-Key", session.key)
	response, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, body
}

// One extra reviewed start supplies a positive HELD control after measuring the
// unchanged base fixture. Its preparation is outside the fresh-process timer.
func largeHeldClaim(t *testing.T, p store.Project, source store.State, bin string) {
	t.Helper()
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	packet := model.PacketRef{CommandID: largeID(800001), Digest: model.HashBytes([]byte("held control"))}
	start, err := model.EncodeEvent(&model.TaskStart{Task: model.RecordRef{Project: p.ID, RecordID: largeID(1), Revision: 1}, AttemptID: largeID(700001), Actor: model.Actor{ID: "contributor"}})
	if err != nil {
		t.Fatal(err)
	}
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "synthetic held control", Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "contributor"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: {State: model.Known, Value: &at}}, EventPackets: []model.ID{packet.CommandID}})
	if err != nil {
		t.Fatal(err)
	}
	head := source.Snapshot().Watermark()
	bundle := model.Bundle{Version: model.WireVersion, Project: p.ID, Sequence: head.Sequence + 1, Predecessor: head.CommandID, CommandID: largeID(900001), RequestDigest: packet.Digest, Admitter: model.Actor{ID: "reviewer"}, RecordedAt: at, Packets: []model.PacketRef{packet}, Events: []model.Event{start, review}}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Ledger, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func() {
		cmd := exec.Command(bin, "check", "claim", string(largeID(700001)), "--json")
		cmd.Dir = p.Root
		out, err := cmd.Output()
		var answer struct{ Status string }
		decodeErr := json.Unmarshal(out, &answer)
		if err != nil || decodeErr != nil || answer.Status != string(query.ClaimHeld) {
			t.Fatalf("positive HELD control: %v %v %s", err, decodeErr, out)
		}
	}
	invoke()
	began := time.Now()
	invoke()
	measureLarge(t, "fresh process HELD check, one reviewed start added", time.Since(began), time.Second)
}
