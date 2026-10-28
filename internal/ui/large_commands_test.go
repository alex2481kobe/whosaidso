// Large synthetic ledgers measure process startup and owner screens. This file
// never uses a checkout's ledger or treats a prebuilt cache as an uncached load.
package ui

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	for _, args := range [][]string{{"todo"}, {"check", "claim", string(largeID(1)), "--json"}} {
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
		measureLarge(t, "fresh process "+args[0]+" with index", time.Since(start), time.Second)
	}

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
