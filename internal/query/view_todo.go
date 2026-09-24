package query

// The todo view: everything owed, in flight first. It
// absorbs the in-flight half of the old now preset (in-flight tasks with every
// run of theirs, blocked-task waits, out-of-scope runs), the open decisions,
// and intake not accepted (every such packet, with the ledger's disposition;
// the totals say which are owed). Each task appears once; the limit cuts only
// READY tasks and says how many it cut. Rendering lives in view_render.go.

import (
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

type TodoAnswer struct {
	ViewHeader
	InFlight           []TodoTask             `json:"in_flight"`
	AwaitingAcceptance []TodoTask             `json:"awaiting_acceptance"`
	Blocked            []TodoTask             `json:"blocked"`
	Ready              []TodoTask             `json:"ready"`
	OpenDecisions      []Detail               `json:"open_decisions"`
	PacketsNotAccepted []Packet               `json:"packets_not_accepted"`
	Attention          []Attention            `json:"attention"`
	Totals             TodoTotals             `json:"totals"`
	Omitted            map[string]LimitReport `json:"omitted"`
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
}

// currentRecords is every current revision, in the snapshot's record order.
func currentRecords(s reduce.Snapshot) []reduce.Record {
	out := []reduce.Record{}
	for _, r := range s.Records() {
		if rev, _ := s.CurrentRevision(reduce.Ident{Project: r.Key.Project, ID: r.Key.ID}); r.Key.Revision == rev {
			out = append(out, r)
		}
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

func todoView(project store.Project, s reduce.Snapshot, h ViewHeader, limit int) (*TodoAnswer, error) {
	intake, err := pending(project, s)
	if err != nil {
		return nil, err
	}
	a := &TodoAnswer{ViewHeader: h, InFlight: []TodoTask{}, AwaitingAcceptance: []TodoTask{}, Blocked: []TodoTask{},
		Ready: []TodoTask{}, OpenDecisions: []Detail{}, PacketsNotAccepted: intake, Attention: []Attention{}}
	d := newDetailer(s)
	owed := []Attention{}
	flying := map[reduce.Ident]int{}
	for _, fact := range currentRecords(s) {
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
	report := LimitReport{Requested: "none", Offered: len(a.Ready)}
	if limit > 0 {
		report.Requested = limit
		if len(a.Ready) > limit {
			report.Omitted, a.Ready = len(a.Ready)-limit, a.Ready[:limit]
		}
	}
	a.Omitted = map[string]LimitReport{"ready": report}
	a.Totals = TodoTotals{InFlight: len(a.InFlight), AwaitingAcceptance: len(a.AwaitingAcceptance), Blocked: len(a.Blocked),
		Ready: report.Offered, OpenDecisions: len(a.OpenDecisions)}
	for _, p := range a.PacketsNotAccepted {
		switch p.Disposition {
		case "pending":
			a.Totals.IntakeUnreviewed++
		case "correction-requested":
			a.Totals.IntakeCorrectionRequested++
		case "rejected":
			a.Totals.IntakeRejected++
		}
	}
	seen := map[model.ID]bool{}
	for _, section := range [][]TodoTask{a.InFlight, a.AwaitingAcceptance, a.Blocked, a.Ready} {
		for _, t := range section {
			seen[t.Ref.RecordID] = true
		}
	}
	a.Totals.Tasks = len(seen) + report.Omitted
	return a, nil
}
