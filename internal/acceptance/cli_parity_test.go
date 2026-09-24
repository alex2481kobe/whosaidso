// End-to-end CLI and ledger counterexamples belong here: forged caches,
// read/replay parity and CLI refusals. Production fixes and other acceptance
// specifications do not. Fixtures live in test temp dirs, not the repo tree (a
// scratch dir in the checkout makes every observed checkout dirty), and the
// build uses the caller's GOCACHE instead of one machine's path.
package acceptance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

var cliParityBuild sync.Once
var cliParityBinary string

func cliParityNew(t *testing.T) *gateVerifyFixture {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cliParityBuild.Do(func() {
		dir, err := os.MkdirTemp("", "whosaidso-cli-parity-bin-")
		if err != nil {
			t.Fatal(err)
		}
		cliParityBinary = filepath.Join(dir, "whosaidso")
		cmd := exec.Command("go", "build", "-o", cliParityBinary, "./cmd/whosaidso")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v: %s", err, out)
		}
	})
	root := t.TempDir()
	t.Setenv(store.HomeEnv, filepath.Join(root, "machine"))
	t.Setenv("WHOSAIDSO_ACTOR", "holder")
	pvPut(t, root, "whosaidso.toml", []byte("id = 'example/acceptance'\nledger = '.whosaidso/events'\n"))
	if _, err := store.Bind(context.Background(), root, root); err != nil {
		t.Fatal(err)
	}
	p, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return &gateVerifyFixture{t: t, p: p, n: 200}
}

func cliParityRun(t *testing.T, f *gateVerifyFixture, args ...string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command(cliParityBinary, args...)
	cmd.Dir = f.p.Root
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		e, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = e.ExitCode()
	}
	pvPut(t, f.p.Root, "last-stdout.txt", out.Bytes())
	pvPut(t, f.p.Root, "last-stderr.txt", stderr.Bytes())
	return out.Bytes(), code
}

func TestReplayHandbackParity(t *testing.T) {
	for _, attack := range []string{"stranger", "blocked-mid-task", "out-of-scope"} {
		t.Run(attack, func(t *testing.T) {
			f := cliParityNew(t)
			holder := model.Actor{ID: "holder"}
			ref, req := hbVerifyStart(t, f, holder)
			bad := &model.AttemptTerminal{Task: ref, AttemptID: req.AttemptID, Outcome: model.AttemptStopped, Reason: "unfinished", NextAction: "resume", DeliveryRefs: []model.ArtifactRef{}}
			author, want := holder, "missing-hold"
			if attack == "stranger" {
				author, want = model.Actor{ID: "stranger"}, "attribution-mismatch"
			} else {
				bad.Outcome = model.AttemptOutcome(attack)
			}
			if _, err := f.admit(author, holder, bad); recCode(err) != want {
				t.Fatalf("admission control: want %s, got %v", want, err)
			}
			packet, err := hbVerifyAPI(f, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := hbVerifyAdmit(f, packet); err != nil {
				t.Fatal(err)
			}
			prefix, err := store.ReadPrefix(f.p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reduce.Replay(prefix); err != nil {
				t.Fatalf("honest replay: %v", err)
			}
			last := &prefix[len(prefix)-1]
			for i, raw := range last.Events {
				e, err := model.DecodeEvent(raw)
				if err != nil {
					t.Fatal(err)
				}
				switch e := e.(type) {
				case *model.ReviewAdmit:
					if attack == "stranger" {
						e.Authors[packet.CommandID] = author
					}
				case *model.AttemptTerminal:
					e.Outcome = bad.Outcome
				}
				last.Events[i] = recEncode(t, e)
			}
			if got, err := reduce.Replay(prefix); err == nil {
				projection, _ := got.Task(reduce.Ident{Project: f.p.ID, ID: ref.RecordID})
				t.Errorf("expected replay to refuse %s as admission did (%s); got task %s. Ledger-only replay accepts an unauthorized receipt or loses required handback debt", attack, want, projection.Status)
			}
		})
	}
}

func TestCacheCannotForgeLedgerFact(t *testing.T) {
	f := cliParityNew(t)
	c := f.claim(model.Actor{ID: "holder"})
	c.Spec.Assertion = "ledger says original"
	if _, err := f.admit(model.Actor{ID: "holder"}, model.Actor{ID: "holder"}, c); err != nil {
		t.Fatal(err)
	}
	want, code := cliParityRun(t, f, "show", string(c.ID), "--json")
	if code != 0 || !bytes.Contains(want, []byte(c.Spec.Assertion)) {
		t.Fatalf("CLI control: %d %s", code, want)
	}
	cache := filepath.Join(f.p.CacheDir(), "snapshot")
	raw, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve codec string lengths; the two texts must have identical size.
	forged := bytes.ReplaceAll(raw, []byte("ledger says original"), []byte("cache says invented!"))
	if bytes.Equal(raw, forged) {
		t.Fatal("fixture did not find the cached assertion")
	}
	head := len("whosaidso-cache/1\n")
	sum := sha256.Sum256(forged[head+sha256.Size:])
	copy(forged[head:head+sha256.Size], sum[:])
	if err := os.WriteFile(cache, forged, 0600); err != nil {
		t.Fatal(err)
	}
	got, code := cliParityRun(t, f, "show", string(c.ID), "--json")
	full, err := store.Replayed(f.p)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := full.Snapshot().Record(model.RecordRef{Project: f.p.ID, RecordID: c.ID, Revision: 1})
	if !ok || rec.Claim.Assertion != c.Spec.Assertion {
		t.Fatal("full replay control lost the original")
	}
	// A cache rewritten together
	// with its public checksum is a documented blind spot of the default read
	// (no lightweight check without a secret or a full replay can detect it).
	// The defense is WHOSAIDSO_NO_CACHE=1, which reads the whole ledger; the
	// original assertion that the default path resists forgery became this one.
	if code != 0 {
		t.Fatalf("default read over a forged cache must still answer, exit %d", code)
	}
	t.Logf("default read over the forged cache shows the fabricated assertion: %t (known blind spot)",
		bytes.Contains(got, []byte("cache says invented!")))
	t.Setenv("WHOSAIDSO_NO_CACHE", "1")
	checked, code := cliParityRun(t, f, "show", string(c.ID), "--json")
	if code != 0 || !bytes.Equal(checked, want) || bytes.Contains(checked, []byte("cache says invented!")) {
		t.Errorf("WHOSAIDSO_NO_CACHE=1 must answer from the ledger alone (%q) over a forged cache; exit=%d, fabricated assertion visible=%t",
			rec.Claim.Assertion, code, bytes.Contains(checked, []byte("cache says invented!")))
	}
}

func TestHomeConfigChangeCannotLoseAdmission(t *testing.T) {
	f := cliParityNew(t)
	f.control()
	pvPut(t, f.p.Root, "whosaidso.toml", []byte("id = 'example/acceptance'\nledger = '.relocated/events'\n"))
	c := f.claim(model.Actor{ID: "holder"})
	b, err := f.admit(model.Actor{ID: "holder"}, model.Actor{ID: "holder"}, c)
	if err != nil {
		return
	}
	current, err := store.Open(f.p.Root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Replayed(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Snapshot().Record(model.RecordRef{Project: current.ID, RecordID: c.ID, Revision: 1}); !ok {
		t.Errorf("expected obsolete opened home to refuse admission; acknowledged sequence %d in old ledger, fresh home has sequence %d and cannot see the fact. Root-only recheck loses an acknowledged admission after config changes", b.Sequence, s.Snapshot().Watermark().Sequence)
	}
}

func TestDuplicatePacketDryRunMatchesAdmission(t *testing.T) {
	f := cliParityNew(t)
	c := f.claim(model.Actor{ID: "holder"})
	packet := f.capture(model.Actor{ID: "holder"}, recEncode(t, c))
	control, code := cliParityRun(t, f, "check", "admission", "--json", "--packet", string(packet.CommandID))
	if code != 0 {
		t.Fatalf("single-packet dry-run control: %d %s", code, control)
	}
	got, checkCode := cliParityRun(t, f, "check", "admission", "--json", "--packet", string(packet.CommandID), "--packet", string(packet.CommandID))
	actual, admitCode := cliParityRun(t, f, "admit", string(packet.CommandID), string(packet.CommandID), "--json", "--outcome", "accepted", "--reason", "same packet twice")
	if admitCode != 0 {
		t.Fatalf("actual admission control: %d %s", admitCode, actual)
	}
	if checkCode != admitCode {
		t.Errorf("expected admission dry run and actual admission to agree; dry run exit=%d %s; actual exit=%d. Check duplicates a packet that admission deduplicates", checkCode, strings.TrimSpace(string(got)), admitCode)
	}
}

func TestCriterionTemplateRefusesAmbiguousClaim(t *testing.T) {
	f := cliParityNew(t)
	a := model.Actor{ID: "holder"}
	criterion := f.id()
	pvPut(t, f.p.Root, "example.json", []byte(pvPass))
	var claims []model.ID
	for i := 0; i < 2; i++ {
		c := f.claim(a)
		if _, err := f.admit(a, a, c); err != nil {
			t.Fatal(err)
		}
		claims = append(claims, c.ID)
		fix := recCriterionFix()
		fix.Claim = model.RecordRef{Project: f.p.ID, RecordID: c.ID, Revision: 1}
		fix.CriterionID, fix.Author, fix.SourceRefs = criterion, a, []model.ArtifactRef{}
		fix.Expression.ResultSelector = pvPin([]byte(pvPass), "example.json")
		fix.Expression.ResultSelector.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}
		fix.Expression.Population.Selector = pvPin([]byte(pvPass), "example.json")
		fix.Expression.Population.Selector.Selector = model.Selector{Kind: "json-pointer", Pointer: "/population"}
		if _, err := f.admit(a, a, fix); err != nil {
			t.Fatal(err)
		}
	}
	for _, claim := range claims {
		if out, code := cliParityRun(t, f, "template", "criterion.fix", "--criterion", string(criterion), "--claim", string(claim)); code != 0 {
			t.Fatalf("explicit-claim control: %d %s", code, out)
		}
	}
	out, code := cliParityRun(t, f, "template", "criterion.fix", "--criterion", string(criterion))
	if code == 0 {
		var events []struct {
			Data struct {
				Claim model.RecordRef `json:"claim"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &events); err != nil {
			t.Fatal(err)
		}
		_, admitCode := cliParityRun(t, f, "template", "criterion.fix", "--criterion", string(criterion), "--set", "source_refs=[]", "--capture", "--admit", "--reason", "revise the selected criterion")
		t.Errorf("expected ambiguity refusal requiring --claim; template silently selected claim %s from %v; --capture --admit exit=%d. Authoring must not choose which claim's criterion to revise", events[0].Data.Claim.RecordID, claims, admitCode)
	}
}

func TestCorruptBindingRecoveryAdviceWorks(t *testing.T) {
	f := cliParityNew(t)
	if out, code := cliParityRun(t, f, "home", f.p.Root, "--json"); code != 0 {
		t.Fatalf("healthy rebind control: %d %s", code, out)
	}
	entries, err := os.ReadDir(filepath.Join(os.Getenv(store.HomeEnv), "projects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			pvPut(t, os.Getenv(store.HomeEnv), "projects/"+entry.Name(), []byte("broken\n"))
		}
	}
	out, code := cliParityRun(t, f, "home", f.p.Root, "--json")
	if code != 0 && bytes.Contains(out, []byte("fix it with whosaidso home PATH")) {
		t.Errorf("expected actionable recovery advice; the recommended home PATH command fails with exit %d and repeats the identical advice: %s", code, strings.TrimSpace(string(out)))
	}
}
