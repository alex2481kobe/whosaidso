package query

// The todo view: everything owed, in flight first. It
// absorbs the in-flight half of the old now preset (in-flight tasks with every
// run of theirs, blocked-task waits, out-of-scope runs), the open decisions,
// and intake not accepted (every such packet, with the ledger's disposition;
// the totals say which are owed). Each task appears once; the limit cuts only
// READY tasks and says how many it cut. Rendering lives in view_render.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type TodoAnswer struct {
	ViewHeader
	ActorFilter        string                 `json:"actor_filter,omitempty"`
	FilterNote         string                 `json:"filter_note,omitempty"`
	InFlight           []TodoTask             `json:"in_flight"`
	AwaitingAcceptance []TodoTask             `json:"awaiting_acceptance"`
	Blocked            []TodoTask             `json:"blocked"`
	Ready              []TodoTask             `json:"ready"`
	OpenDecisions      []Detail               `json:"open_decisions"`
	PacketsNotAccepted []Packet               `json:"packets_not_accepted"`
	Attention          []Attention            `json:"attention"`
	Totals             TodoTotals             `json:"totals"`
	Omitted            map[string]LimitReport `json:"omitted"`
	// SetAside lists every bundle replay set aside after a merge. It is absent
	// when there is none, so a ledger that never clashed answers exactly as
	// before chains could split.
	SetAside []SetAsideBundle `json:"set_aside,omitempty"`
}

// TodoTask is one owed task. Accepter is set for a task awaiting acceptance:
// the named accepter, or "anyone" when the task names none. Runs is set for an
// in-flight task: every run of that task, sealed or not, earlier attempts too,
// as the run view (duration, outcome, scope); the admitted envelopes behind
// each run are in show and continue, which keeps todo's answer small.
type TodoTask struct {
	Detail
	Accepter any        `json:"accepter,omitempty"`
	Runs     *[]RunView `json:"runs,omitempty"`
	// Claims is set for an in-flight task: each live claim, its holder, when
	// it was claimed and last worked on (view_board.go).
	Claims []BoardClaim `json:"claims,omitempty"`
}

// TodoTotals counts each owed task identity once, whichever section holds it;
// Ready counts every READY task, including any the limit omitted. The intake
// counts split packets_not_accepted by what the ledger's review says: an unreviewed
// packet awaits review, a correction-requested one had a correction asked for
// (the ledger does not link a corrected packet to the request, so whether one
// answered it is UNKNOWN, never assumed owed or done), and a rejected one owes
// nothing.
type TodoTotals struct {
	Tasks                     int `json:"tasks"`
	InFlight                  int `json:"in_flight"`
	AwaitingAcceptance        int `json:"awaiting_acceptance"`
	Blocked                   int `json:"blocked"`
	Ready                     int `json:"ready"`
	OpenDecisions             int `json:"open_decisions"`
	IntakeUnreviewed          int `json:"intake_unreviewed"`
	IntakeCorrectionRequested int `json:"intake_correction_requested"`
	IntakeRejected            int `json:"intake_rejected"`
	// IntakeElsewhere counts pending packets another checkout of this project
	// captured on this machine; absent when none.
	IntakeElsewhere int `json:"intake_elsewhere,omitempty"`
	SetAside        int `json:"set_aside,omitempty"`
	// SetAsideResolved counts set-aside bundles a setaside.resolve dealt
	// with, and IntakeSetAside pending packets a set-aside bundle carried:
	// admitted on a branch, not to be admitted again. Absent when none.
	SetAsideResolved int `json:"set_aside_resolved,omitempty"`
	IntakeSetAside   int `json:"intake_set_aside,omitempty"`
	// StaleClaims counts live claims idle past stale_after; absent when none.
	StaleClaims int `json:"stale_claims,omitempty"`
}

// currentRecords is every current revision, in the snapshot's record order.
func currentRecords(s reduce.Snapshot) []reduce.Record {
	out := []reduce.Record{}
	for _, head := range s.RecordHeads() {
		r, _ := s.Current(reduce.Ident{Project: head.Key.Project, ID: head.Key.ID})
		out = append(out, r)
	}
	return out
}

func hasReasonKind(reasons []reduce.BlockedReason, kind string) bool {
	for _, reason := range reasons {
		if reason.Kind == kind {
			return true
		}
	}
	return false
}

func accepterOf(spec *model.TaskSpec) any {
	if spec.Accepter == nil {
		return "anyone"
	}
	return actor(*spec.Accepter)
}

func todoView(project store.Project, s reduce.Snapshot, h ViewHeader, limit int, now time.Time, actorFilter string, brief bool) (*TodoAnswer, error) {
	intake, err := pending(project, s)
	if err != nil {
		return nil, err
	}
	a := &TodoAnswer{ViewHeader: h, InFlight: []TodoTask{}, AwaitingAcceptance: []TodoTask{}, Blocked: []TodoTask{},
		Ready: []TodoTask{}, OpenDecisions: []Detail{}, PacketsNotAccepted: intake, Attention: []Attention{}}
	d := newDetailer(s, !brief)
	owed := []Attention{}
	staleClaims := 0
	flying := map[reduce.Ident]int{}
	for _, head := range s.RecordHeads() {
		if head.Kind != model.Task && head.Kind != model.Decision {
			continue
		}
		fact, _ := s.Current(reduce.Ident{Project: head.Key.Project, ID: head.Key.ID})
		if fact.Kind == model.Decision {
			if v, _ := d.detail(fact); v.Decision != nil && v.Decision.Status == reduce.StatusOpen {
				a.OpenDecisions = append(a.OpenDecisions, v)
			}
			continue
		}
		if fact.Kind != model.Task {
			continue
		}
		v, _ := d.detail(fact)
		t := TodoTask{Detail: v}
		switch v.Task.Status {
		case reduce.StatusInFlight:
			t.Runs = &[]RunView{}
			var stale []Attention
			t.Claims, stale = boardClaims(s, v, project, now)
			owed = append(owed, stale...)
			staleClaims += len(stale)
			flying[reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}] = len(a.InFlight)
			a.InFlight = append(a.InFlight, t)
		case reduce.StatusBlocked:
			owed = append(owed, owedBy(v.Ref, v.Label, v.Task.Reasons)...)
			if hasReasonKind(v.Task.Reasons, reduce.ReasonAwaitingAcceptance) {
				t.Accepter = accepterOf(fact.Task)
				a.AwaitingAcceptance = append(a.AwaitingAcceptance, t)
			} else {
				a.Blocked = append(a.Blocked, t)
			}
		case reduce.StatusReady:
			a.Ready = append(a.Ready, t)
		}
	}
	for _, inv := range s.Invocations() {
		at, ok := flying[reduce.Ident{Project: inv.Attempt.Project, ID: inv.Attempt.Task}]
		if !ok {
			continue
		}
		run := runView(s, inv)
		*a.InFlight[at].Runs = append(*a.InFlight[at].Runs, run)
		if note, outside := runAttention(run); outside {
			a.Attention = append(a.Attention, note)
		}
	}
	a.Attention = append(a.Attention, owed...)
	a.Attention = append(a.Attention, idleActors(a, project.Policy.Team)...)
	report := LimitReport{Requested: "none", Offered: len(a.Ready)}
	if limit > 0 && actorFilter == "" {
		report.Requested = limit
		if len(a.Ready) > limit {
			report.Omitted, a.Ready = len(a.Ready)-limit, a.Ready[:limit]
		}
	}
	a.Omitted = map[string]LimitReport{"ready": report}
	a.Totals = TodoTotals{InFlight: len(a.InFlight), AwaitingAcceptance: len(a.AwaitingAcceptance), Blocked: len(a.Blocked),
		Ready: report.Offered, OpenDecisions: len(a.OpenDecisions), StaleClaims: staleClaims}
	var resolved int
	var asidePackets map[model.ID]bool
	a.SetAside, resolved, asidePackets = setAsideBundles(s)
	a.Totals.SetAside, a.Totals.SetAsideResolved = len(a.SetAside), resolved
	for i, p := range a.PacketsNotAccepted {
		if asidePackets[p.CommandID] && (p.Disposition == "pending" || p.Disposition == "captured-elsewhere") {
			a.PacketsNotAccepted[i].Disposition, p.Disposition = "set-aside", "set-aside"
		}
		switch p.Disposition {
		case "set-aside":
			a.Totals.IntakeSetAside++
		case "pending":
			a.Totals.IntakeUnreviewed++
		case "correction-requested":
			a.Totals.IntakeCorrectionRequested++
		case "rejected":
			a.Totals.IntakeRejected++
		case "captured-elsewhere":
			a.Totals.IntakeElsewhere++
		}
	}
	seen := map[model.ID]bool{}
	for _, section := range [][]TodoTask{a.InFlight, a.AwaitingAcceptance, a.Blocked, a.Ready} {
		for _, t := range section {
			seen[t.Ref.RecordID] = true
		}
	}
	a.Totals.Tasks = len(seen) + report.Omitted
	if actorFilter != "" {
		filterTodo(a, actorFilter, limit)
	}
	return a, nil
}

// setAsideBundles lists every set-aside bundle not yet resolved, with what to
// do about it and the command that records it was dealt with, counts the
// resolved ones, and names the packets set-aside bundles carried, so todo
// does not offer them as intake to admit again. It reads what replay kept of
// each set-aside bundle, never the bundles.
func setAsideBundles(s reduce.Snapshot) ([]SetAsideBundle, int, map[model.ID]bool) {
	aside := s.SetAside()
	if len(aside) == 0 {
		return nil, 0, nil
	}
	claims, packets := map[model.BundleRef][]claimEvent{}, map[model.ID]bool{}
	for _, v := range aside {
		ref := model.BundleRef{Sequence: v.Sequence, CommandID: v.CommandID}
		for _, c := range v.Claims {
			claims[ref] = append(claims[ref], claimEvent{attempt: c.Attempt, task: c.Task, actor: c.Actor})
		}
		for _, id := range v.Packets {
			packets[id] = true
		}
	}
	out, resolved := []SetAsideBundle{}, 0
	for _, v := range aside {
		if v.Resolved != nil {
			resolved++
			continue
		}
		entry := SetAsideBundle{SetAside: v, Next: setAsideNext[v.Code], Resolve: fmt.Sprintf(
			"whosaidso template setaside.resolve --set bundle.sequence=%d --set bundle.command_id=%s --set reason=\"...\" --capture --admit --reason \"...\"",
			v.Sequence, v.CommandID)}
		if entry.Next == "" {
			entry.Next = reRecord
		}
		if lost := claims[model.BundleRef{Sequence: v.Sequence, CommandID: v.CommandID}]; len(lost) > 0 {
			entry.Next = lostClaimNext(lost)
		}
		out = append(out, entry)
	}
	return out, resolved, packets
}

// lostClaimNext tells the holder of each claim a set-aside bundle carried
// that it lost the task to the branch merged first.
func lostClaimNext(lost []claimEvent) string {
	parts := make([]string, 0, len(lost))
	for _, c := range lost {
		parts = append(parts, fmt.Sprintf("attempt %s by %s", c.attempt, actorName(c.actor)))
	}
	return "lost claim: " + strings.Join(parts, ", ") + " lost the task to a claim on a branch merged first; " +
		"its holder stops and hands nothing back on it (whosaidso check claim ATTEMPT_ID names who holds the task now), then resolves it"
}

// SetAsideBundle is one bundle replay kept but did not apply: the fault that
// set it aside, what to do about it, and how to record that it was dealt with.
type SetAsideBundle struct {
	reduce.SetAside
	Next    string `json:"next"`
	Resolve string `json:"resolve"`
}

// setAsideNext is what to do about a bundle set aside for each clash code
// whose remedy is not re-recording its change.
var setAsideNext = map[string]string{
	reduce.CodeDuplicateSourceOrder: "one message captured on two branches: if it is the same message, it is already in the ledger and nothing is lost; " +
		"if it is another, capture it again with its own order; then resolve",
	reduce.CodeCompetingDisposition: "the decision was disposed on a branch merged first, and that disposition stands; " +
		"if it should not, the owner rules again as a new disposition; then resolve",
	reduce.CodeAuthorityUnavailable: "the words this act cited were set aside with their own bundle, so the act was too: " +
		"deal with that bundle first, then record the act again citing words that stand; then resolve",
}

const reRecord = "re-record it: the bundle stays in the ledger and is never applied, so capture its change again " +
	"against the current records (an amendment from the current revision) and admit it; then resolve"
