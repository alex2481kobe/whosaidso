package acceptance_test

// Independent attacks on the multi-writer rules: the board's stale judgment,
// a pending packet's origin, a departed holder's receipt authority, the
// self-admission policy, branch clashes and their resolution, claim answers,
// cached reads and check ledger. Each test opens with a control that passes.
// Tests that read in process set WHOSAIDSO_HOME, so they are not parallel.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// reviewWorld is a flow project whose home is set in this process too.
func reviewWorld(t *testing.T, toml string) *flowWorld {
	t.Helper()
	w := flowNew(t)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(w.home, "ws"))
	w.put("whosaidso.toml", []byte("id = \"flow/review\"\nledger = \".whosaidso/events\"\n"+toml))
	return w
}

// todoAt reads todo in process with the reader's clock set to now.
func (w *flowWorld) todoAt(now time.Time) *query.TodoAnswer {
	w.t.Helper()
	project, err := store.Discover(w.root)
	if err != nil {
		w.t.Fatal(err)
	}
	answer, err := query.ReadView(project, query.ViewRequest{View: "todo", Now: now})
	if err != nil {
		w.t.Fatal(err)
	}
	return answer.(*query.TodoAnswer)
}

func staleNotes(a *query.TodoAnswer) (out []string) {
	for _, n := range a.Attention {
		if n.Kind == "stale-claim" {
			out = append(out, n.Reason)
		}
	}
	return out
}

func (w *flowWorld) claimAnswer(attempt model.ID) query.ClaimAnswer {
	w.t.Helper()
	out, _ := w.cli(nil, "check", "claim", "--json", string(attempt))
	var a query.ClaimAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		w.t.Fatalf("check claim --json printed %q: %v", out, err)
	}
	return a
}

func (w *flowWorld) takeOver(actor string, task model.RecordRef, prior model.ID) model.ID {
	w.t.Helper()
	file, body := "confirmations/"+string(prior)+".json", []byte(`{"stopped":"`+string(prior)+`"}`)
	w.put(file, body)
	next := w.id()
	w.mustAdmit(actor, &model.TaskTakeover{Task: task, Actor: model.Actor{ID: actor}, AttemptID: next, PriorAttemptID: prior, StoppedConfirmationRef: pvPin(body, file)})
	return next
}

// A claim idle 90s is past stale_after '1m'. The idle time is truncated to
// whole minutes before the comparison, so it is flagged only after twice the
// limit here, and a limit under a minute never flags before a full minute.
func TestReviewStaleIsJudgedAgainstTheConfiguredThreshold(t *testing.T) {
	w := reviewWorld(t, "stale_after = '1m'\n")
	w.task("claimed and then quiet")
	fresh := w.todoAt(time.Now().UTC())
	if len(fresh.InFlight) != 1 || len(fresh.InFlight[0].Claims) != 1 || len(staleNotes(fresh)) != 0 {
		t.Fatalf("control: a fresh claim is listed and not stale: %+v", fresh.InFlight)
	}
	last := fresh.InFlight[0].Claims[0].LastActivityAt
	if notes := staleNotes(w.todoAt(last.Add(3 * time.Minute))); len(notes) != 1 {
		t.Fatalf("control: idle 3m under stale_after 1m is stale, got %v", notes)
	}
	got := w.todoAt(last.Add(90 * time.Second))
	if c := got.InFlight[0].Claims[0]; c.Stale == nil || !*c.Stale || got.Totals.StaleClaims != 1 {
		t.Errorf("expected a claim idle 90s stale under stale_after '1m'; got stale=%t total=%d (view_board.go truncates idle to minutes before comparing)",
			c.Stale != nil && *c.Stale, got.Totals.StaleClaims)
	}
}

// Claim times come from the admitting machine's clock, the reader's from its
// own: a claim dated after the reader's now is judged, and not stale. (Adapted
// when idle_for, which printed a negative time here, was removed.)
func TestReviewBoardNeverPrintsNegativeIdleTime(t *testing.T) {
	w := reviewWorld(t, "stale_after = '1m'\n")
	w.task("claimed on a machine whose clock runs ahead")
	last := w.todoAt(time.Now().UTC()).InFlight[0].Claims[0].LastActivityAt
	if c := w.todoAt(last.Add(-5 * time.Minute)).InFlight[0].Claims[0]; c.Stale == nil || *c.Stale {
		t.Errorf("expected a claim dated after the reader's clock judged and not stale; got %+v", c.Stale)
	}
}

// Taking a stale claim over is the remedy the stale note names. The replaced
// attempt stays live by design, but check claim reads it TAKEN-OVER, so the
// board must not keep calling it a takeover candidate.
func TestReviewTakenOverClaimIsNotAStaleCandidate(t *testing.T) {
	w := reviewWorld(t, "")
	task := w.task("work a stale holder abandoned")
	second := w.takeOver("agent2", task.ref, task.attempt)
	if a := w.claimAnswer(task.attempt); a.Status != query.ClaimTakenOver {
		t.Fatalf("control: the replaced claim reads TAKEN-OVER, got %s", a.Status)
	}
	notes := staleNotes(w.todoAt(time.Now().UTC().Add(48 * time.Hour)))
	named := func(id model.ID) bool { return strings.Contains(strings.Join(notes, "\n"), "attempt "+string(id)+" ") }
	if !named(second) {
		t.Fatalf("control: the current holder, idle 48h, is flagged stale: %v", notes)
	}
	if named(task.attempt) {
		t.Errorf("expected no stale-claim note for attempt %s, which check claim reads TAKEN-OVER: the note's own remedy never clears it: %v", task.attempt, notes)
	}
}

// check claim names the attempt that holds the task instead of a replaced
// claim: through a chain of takeovers that is the last one, and once the last
// taker has ended it is none of the replaced attempts (as for a lost claim).
func TestReviewTakenOverClaimNamesWhoHoldsTheTaskNow(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("work passed along")
	second := w.takeOver("agent2", task.ref, task.attempt)
	if a := w.claimAnswer(task.attempt); a.Status != query.ClaimTakenOver || a.Current == nil || a.Current.Attempt != second {
		t.Fatalf("control: one takeover names the taker as holder: %+v", a)
	}
	third := w.takeOver("agent3", task.ref, second)
	if a := w.claimAnswer(task.attempt); a.Current == nil || a.Current.Attempt != third {
		t.Errorf("expected the answer to name attempt %s, who holds the task now; got %+v (claim.go appliedClaim names the first taker, itself replaced)", third, a.Current)
	}
	out, err := w.cli(nil, "handback", "--json", "--command-id", string(w.id()), "--actor", "agent3", "--attempt-id", string(third),
		"--outcome", "stopped", "--reason", "done for now", "--next-action", "claim it again")
	if err != nil || w.review("accepted", flowPacket(t, out)) != nil {
		t.Fatalf("control: the last taker's receipt admits: %v", err)
	}
	if a := w.claimAnswer(task.attempt); a.Current != nil {
		t.Errorf("expected no holder named once the last taker has ended; got %+v, next %q", a.Current, a.Next)
	}
}

// Moving a checkout must not strand its pending intake (config.go: moving a
// checkout does not lose its intake): the origin note is the checkout's path,
// so a renamed directory reads its own packet as another checkout's.
func TestReviewPendingPacketSurvivesMovingItsCheckout(t *testing.T) {
	w := reviewWorld(t, "")
	packet := w.capture(flowAgent, w.mwClaimEvent(w.id(), "captured, not yet reviewed"))
	disposition := func() string {
		for _, p := range w.todo().PacketsNotAccepted {
			if p.CommandID == packet {
				return p.Disposition
			}
		}
		return "absent"
	}
	if d := disposition(); d != "pending" {
		t.Fatalf("control: the packet is pending in its own checkout, got %s", d)
	}
	moved := filepath.Join(t.TempDir(), "moved-checkout")
	if err := os.Rename(w.root, moved); err != nil {
		t.Fatal(err)
	}
	w.root = moved
	if d := disposition(); d != "pending" {
		t.Errorf("expected the packet still pending after its checkout moved; got %q, so no checkout lists it as its own (intake_origin.go keys origin on the path)", d)
	}
}

// reviewForks is a ledger holding a task and an open decision, and two
// machines that each checked it out and admit on their own.
func reviewForks(t *testing.T) (base, a, b *flowWorld, task, decision model.RecordRef, ruling model.ArtifactRef) {
	t.Helper()
	base = mwWorld(t, 100)
	task = base.createTask("shared work").ref
	decision, ruling = base.decision()
	a, b = mwFork(t, base, 300), mwFork(t, base, 500)
	for _, w := range []*flowWorld{a, b} {
		w.put("rulings/ship.json", []byte(`{"ruling":"ship revision one"}`))
	}
	return base, a, b, task, decision, ruling
}

func asideCodes(w *flowWorld) (codes []string) {
	for _, v := range w.todo().SetAside {
		codes = append(codes, v.Code)
	}
	return codes
}

// copyArtifacts adds from's artifact store to to's, as a git merge does.
func copyArtifacts(t *testing.T, from, to *flowWorld) {
	t.Helper()
	root := filepath.Join(from.root, ".whosaidso", "artifacts")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		rel, _ := filepath.Rel(from.root, path)
		if err == nil {
			to.put(filepath.ToSlash(rel), body)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Two branches deciding one decision apart is a clash; the same decision
// ruled twice on one line is not. Two unknown speakers' message order never
// matches; the owner's message 5 on three branches leaves two set aside.
func TestReviewBranchClashesAreSetAsideOnlyAcrossBranches(t *testing.T) {
	t.Parallel()
	base, a, b, task, decision, ruling := reviewForks(t)
	line := mwFork(t, base, 900)
	line.put("rulings/ship.json", []byte(`{"ruling":"ship revision one"}`))
	line.mustAdmit("scribe", line.dispose(decision, ruling, "ship revision one", "owner"))
	line.mustAdmit("scribe", line.dispose(decision, ruling, "ship revision one", "owner"))
	if codes := asideCodes(line); len(codes) != 0 {
		t.Fatalf("control: two dispositions on one line both stand, got %v", codes)
	}
	a.mustAdmit("scribe", a.dispose(decision, ruling, "ship revision one", "owner"))
	b.mustAdmit("scribe-b", b.dispose(decision, ruling, "ship revision one", "owner"))
	if codes := asideCodes(mwMerge(t, a, b)); len(codes) != 1 || codes[0] != "competing-disposition" {
		t.Errorf("expected one competing-disposition set aside for a decision disposed on two branches, got %v", codes)
	}
	for _, w := range []*flowWorld{a, b} {
		body := []byte(`{"note":"` + filepath.Base(w.root) + `"}`)
		w.put("notes/n.json", body)
		ref := pvPin(body, "notes/n.json")
		w.mustAdmit("clerk", &model.SourceIntake{SourceID: w.id(), SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
			Speaker: model.Actor{UnknownReason: "unsigned"}, Order: 5, Referents: []model.RecordRef{task}})
	}
	if codes := asideCodes(mwMerge(t, a, b)); len(codes) != 1 {
		t.Errorf("two unknown speakers' order 5 never match: only the disposition clash is set aside, got %v", codes)
	}
	var three []*flowWorld
	for i := 0; i < 3; i++ {
		w := mwFork(t, base, 600+100*i)
		ownerMessage(w, "owner/"+strconv.Itoa(i)+".json", 5, task)
		three = append(three, w)
	}
	if codes := asideCodes(mwMerge(t, three...)); len(codes) != 2 || codes[0] != "duplicate-source-order" || codes[1] != "duplicate-source-order" {
		t.Errorf("expected message 5 on three branches to leave two duplicate-source-order set aside, got %v", codes)
	}
}

// setaside.resolve never refuses (a repeat, an applied bundle, a bundle that
// does not exist), and the first resolution in ledger order is the one kept.
func TestReviewSetAsideResolveKeepsTheFirstAndNeverRefuses(t *testing.T) {
	base, a, b, task, _, _ := reviewForks(t)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(base.home, "ws"))
	ownerMessage(a, "owner/a.json", 5, task)
	ownerMessage(b, "owner/b.json", 5, task)
	m := mwMerge(t, a, b)
	copyArtifacts(t, a, m)
	copyArtifacts(t, b, m)
	aside := m.todo().SetAside
	if len(aside) != 1 {
		t.Fatalf("control: the clash is set aside, got %+v", aside)
	}
	fork := func(n int) *flowWorld {
		f := mwWorld(t, n)
		mwCopy(t, mwBundles(m), f)
		copyArtifacts(t, m, f)
		return f
	}
	resolve := func(w *flowWorld, seq uint64, id model.ID, reason string) error {
		_, err := w.cli(nil, "template", "setaside.resolve", "--actor", "coordinator", "--set", "bundle.sequence="+strconv.FormatUint(seq, 10),
			"--set", "bundle.command_id="+string(id), "--set", "reason="+reason, "--capture", "--admit", "--reason", reason)
		return err
	}
	f1, f2 := fork(1100), fork(1300)
	for i, w := range []*flowWorld{f1, f2} {
		if err := resolve(w, aside[0].Sequence, aside[0].CommandID, []string{"reason-one", "reason-two"}[i]); err != nil {
			t.Fatalf("control: the printed resolution admits: %v", err)
		}
	}
	applied := strings.TrimSuffix(mwFiles(base)[0], ".json") // 00000001-COMMANDID
	if err := resolve(f1, 999, recID(742), "no such bundle"); err != nil {
		t.Errorf("a resolution naming no bundle must admit as a no-op: %v", err)
	}
	if err := resolve(f1, 1, model.ID(applied[9:]), "an applied bundle"); err != nil {
		t.Errorf("a resolution naming an applied bundle must admit as a no-op: %v", err)
	}
	both := mwMerge(t, f1, f2)
	if left := both.todo(); len(left.SetAside) != 0 || left.Totals.SetAsideResolved != 1 {
		t.Fatalf("both resolved the one bundle: want none owed, one resolved, got %+v %+v", left.SetAside, left.Totals)
	}
	want := ""
	for _, name := range mwFiles(both) { // ledger order
		for _, reason := range []string{"reason-one", "reason-two"} {
			if want == "" && strings.Contains(string(mwBundles(both)[name]), `"`+reason+`"`) {
				want = reason
			}
		}
	}
	project, _ := store.Discover(both.root)
	state, err := store.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if set := state.Snapshot().SetAside(); len(set) != 1 || set[0].Resolved == nil || set[0].Resolved.Reason != want {
		t.Errorf("expected the first resolution in ledger order (%q) kept; got %+v", want, set)
	}
}

// A receipt for another holder's attempt stands on the words of a
// source.intake. A merge that sets that source aside (its message order was
// taken on another branch) removes the words from every answer, yet the
// receipt is a separate bundle and replay never re-checks its carrier: the
// departed holder's claim ends on authority the ledger no longer holds.
func TestReviewReceiptAuthorityDoesNotOutliveItsSetAsideSource(t *testing.T) {
	t.Parallel()
	base := mwWorld(t, 100)
	held := base.task("work a departed agent held")
	elsewhere := base.createTask("another task the owner spoke about").ref
	a, b := mwFork(t, base, 300), mwFork(t, base, 500)
	ownerMessage(a, "owner/a.json", 5, elsewhere) // the owner's message 5 as machine A recorded it
	body := []byte(`{"ruling":"release the departed agent's attempts"}`)
	b.put("owner/b.json", body)
	ref, source := pvPin(body, "owner/b.json"), b.id()
	b.mustAdmit("clerk", &model.SourceIntake{SourceID: source, SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
		Speaker: model.Actor{ID: "owner"}, Order: 5, Referents: []model.RecordRef{held.ref}})
	if err := b.releaseAs("coordinator", held.attempt, source); err != nil {
		t.Fatalf("control: on its own branch the receipt on the owner's word admits: %v", err)
	}
	if status, _ := b.attemptClaim(held.attempt); status != query.ClaimEnded {
		t.Fatalf("control: the branch reads the claim ENDED, got %s", status)
	}
	m := mwMerge(t, a, b)
	aside := m.todo().SetAside
	if len(aside) == 0 || aside[0].Code != "duplicate-source-order" {
		t.Fatalf("control: the merge sets B's message 5 aside, got %+v", aside)
	}
	if status, _ := m.attemptClaim(held.attempt); status == query.ClaimEnded {
		t.Errorf("expected the claim not ENDED once the only words authorizing its release are set aside (or the receipt set aside with them); got %s with %d set-aside "+
			"bundle(s). reduce checkReceiptAuthor never re-checks the carrier on replay: the gate's check holds at one entrance", status, len(aside))
	}
}

// self_admission = 'refuse' reaches every entrance that admits (admit, and
// capture --admit under the author's own name), judges every packet of one
// admission, and leaves rejection open.
func TestReviewRefusedSelfAdmissionHoldsAtEveryEntrance(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	w.put("whosaidso.toml", []byte("id = \"flow/review\"\nledger = \".whosaidso/events\"\nself_admission = \"refuse\"\n"))
	admit := func(actor, outcome string, packets ...model.ID) error {
		args := []string{"admit", "--json", "--command-id", string(w.id()), "--outcome", outcome, "--reason", "review"}
		if actor != "" {
			args = append(args, "--actor", actor)
		}
		for _, p := range packets {
			args = append(args, string(p))
		}
		_, err := w.cli(nil, args...)
		return err
	}
	claim := func(author string) model.ID { return w.capture(author, w.mwClaimEvent(w.id(), author+"'s claim")) }
	if err := admit("reviewer", "accepted", claim("worker"), claim("other")); err != nil {
		t.Fatalf("control: a third known actor admits both packets: %v", err)
	}
	mine, theirs := claim("worker"), claim("other")
	w.refused("two packets whose second is the admitter's own", func() error { return admit("other", "accepted", mine, theirs) })
	w.refused("no admitter named", func() error { return admit("", "accepted", mine) })
	events, _ := model.Encode([]model.Event{recEncode(t, w.mwClaimEvent(w.id(), "self"))})
	w.refused("capture --admit under the author's own name", func() error {
		_, err := w.cli(events, "capture", "--json", "--actor", "worker", "--command-id", string(w.id()), "--events", "-", "--admit", "--reason", "mine")
		return err
	})
	if err := admit("worker", "rejected", mine); err != nil {
		t.Errorf("rejection stays open to the author: %v", err)
	}
}

// A cached read equals a full replay on what this change added: a merge that
// arrives after the cache was built, a set-aside and its resolution, an
// author correction, a receipt's authority and a claim's times.
func TestReviewCachedReadsEqualFullReplayAfterAMerge(t *testing.T) {
	base, a, b, task, _, _ := reviewForks(t)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(base.home, "ws"))
	held := a.task("work a departed agent held")
	reads := func() map[string]string {
		out := map[string]string{}
		for _, v := range [][]string{{"todo", "--json"}, {"history", "--json"}, {"show", "--json"}} {
			got, err := a.cli(nil, v...)
			if err != nil {
				t.Fatalf("%v: %v", v, err)
			}
			out[strings.Join(v, " ")] = string(got)
		}
		return out
	}
	same := func(step string) {
		t.Helper()
		cached := reads()
		t.Setenv("WHOSAIDSO_NO_CACHE", "1")
		full := reads()
		os.Unsetenv("WHOSAIDSO_NO_CACHE")
		for k := range cached {
			if cached[k] != full[k] {
				t.Errorf("%s: %s differs between the cache and a full replay:\ncached: %.500s\nfull:   %.500s", step, k, cached[k], full[k])
			}
		}
	}
	same("before the merge (control)")
	ownerMessage(a, "owner/a.json", 5, task)
	ownerMessage(b, "owner/b.json", 5, task)
	if err := a.releaseAs("coordinator", held.attempt, a.ownerSays("owner", "rulings/release.json", held.ref)); err != nil {
		t.Fatal(err)
	}
	written := a.capture(flowAgent, a.mwClaimEvent(a.id(), "written by the clerk"))
	if err := a.review("accepted", written); err != nil {
		t.Fatal(err)
	}
	a.mustAdmit("clerk", &model.AuthorCorrect{Packets: []model.ID{written}, Writer: model.Actor{ID: "clerk"}, Reason: "clerk wrote it"})
	if h := reads()["history --json"]; !strings.Contains(h, `"written_by"`) || !strings.Contains(h, `"authority"`) {
		t.Fatalf("control: history carries the writer and the receipt's authority: %.300s", h)
	}
	same("on the branch")
	mwCopy(t, mwBundles(b), a)
	copyArtifacts(t, b, a)
	same("after the other branch merged in")
	if len(a.todo().SetAside) != 1 {
		t.Fatalf("control: the merge set the duplicate aside, got %+v", a.todo().SetAside)
	}
	a.resolveAll("coordinator", "the same message")
	same("after the resolution")
}

// check ledger in a project below the repository top: the committed tree is
// read from the repository; a committed artifact whose bytes are not the
// pinned ones fails the check.
func TestReviewCheckLedgerInAProjectBelowTheRepositoryTop(t *testing.T) {
	t.Parallel()
	repo := nestRepo(t)
	w := flowNew(t)
	w.root = filepath.Join(repo, "apps", "foo")
	w.put("whosaidso.toml", []byte("id = \"flow/review\"\nledger = \".whosaidso/events\"\n"))
	w.task("nested work")
	commit := func(msg string) {
		nestGit(t, repo, "add", "--", "apps/foo/whosaidso.toml", "apps/foo/.whosaidso")
		nestGit(t, repo, "commit", "-q", "-m", msg)
	}
	commit("ledger below the top")
	if a, code := checkLedger(t, w.root, w.home); !a.Clean || code != 0 || a.Bundles == 0 {
		t.Fatalf("control: a committed nested ledger is clean: exit %d %+v", code, a)
	}
	w.task("more nested work")
	if a, code := checkLedger(t, w.root, w.home); a.Clean || code != 1 || len(a.NotCommitted) != 2 {
		t.Errorf("expected two uncommitted bundles reported for the nested project, got exit %d %+v", code, a)
	}
	commit("second")
	body := []byte(`{"ruling":"keep the pose sweep"}`)
	w.put("rulings/keep.json", body)
	ref := pvPin(body, "rulings/keep.json")
	w.mustAdmit("clerk", &model.SourceIntake{SourceID: w.id(), SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
		Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{}})
	commit("source")
	if a, code := checkLedger(t, w.root, w.home); !a.Clean || code != 0 {
		t.Fatalf("control: the nested ledger with its artifact committed is clean: exit %d %+v", code, a)
	}
	artifact := filepath.Join(w.root, ".whosaidso", "artifacts", string(ref.Content.SHA256))
	if err := os.WriteFile(artifact, []byte(`{"ruling":"drop the pose sweep"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	commit("a different ruling under the same digest")
	if a, code := checkLedger(t, w.root, w.home); a.Clean || code != 1 {
		t.Errorf("expected check ledger to fail on committed artifact bytes that are not the pinned ones; got exit %d %+v", code, a)
	}
}
