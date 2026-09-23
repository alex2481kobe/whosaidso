package query

// The show view (COMMAND-SPEC §3.3). show ID describes one record, read
// directly: it never builds the whole project. Bare show opens with a summary,
// then every current record grouped by kind, then the whole run inventory,
// unsealed runs included; it absorbs the old state preset. --kind restricts
// bare show to one kind, and for instruments keeps their validation and trust
// attention (the old instruments preset). --stale adds the stale-claims check.

import (
	"sort"

	"datum/internal/model"
	"datum/internal/reduce"
)

type ShowAnswer struct {
	ViewHeader
	Summary   *Summary      `json:"summary,omitempty"` // bare show only
	Records   []Detail      `json:"records"`
	Runs      *[]RunDetail  `json:"runs,omitempty"` // every run bare; the runs its claims observe otherwise
	Attention []Attention   `json:"attention"`
	Stale     *StaleSection `json:"stale,omitempty"` // with --stale only
}

// StaleSection is the stale-claims check with the blind spot it cannot see past.
type StaleSection struct {
	BlindSpot string       `json:"blind_spot"`
	Claims    []StaleClaim `json:"claims"`
}

const staleBlindSpot = "HEAD only: compares each observed claim's last run commit with the invoking checkout's HEAD; uncommitted changes are invisible to it"

// Summary counts what bare show lists. A kind that was not shown is absent,
// not zero. Owed counts the todo sections' tasks and decisions (bare only).
type Summary struct {
	Kind        string          `json:"kind"` // "all", or the one kind shown
	Records     int             `json:"records"`
	Tasks       *map[string]int `json:"tasks,omitempty"`       // by status
	Claims      *map[string]int `json:"claims,omitempty"`      // by status
	Decisions   *map[string]int `json:"decisions,omitempty"`   // by status
	Instruments *map[string]int `json:"instruments,omitempty"` // by validation and by trust
	Runs        *RunCounts      `json:"runs,omitempty"`
	Owed        *OwedCounts     `json:"owed,omitempty"`
	Attention   int             `json:"attention"`
}

type RunCounts struct {
	Total    int `json:"total"`
	Sealed   int `json:"sealed"`
	Unsealed int `json:"unsealed"`
}

type OwedCounts struct {
	InFlight           int `json:"in_flight"`
	AwaitingAcceptance int `json:"awaiting_acceptance"`
	Blocked            int `json:"blocked"`
	Ready              int `json:"ready"`
	OpenDecisions      int `json:"open_decisions"`
}

var kindOrder = map[model.Kind]int{model.Task: 0, model.Claim: 1, model.Decision: 2, model.Instrument: 3}

// showOne describes the one current record, and the runs its claim observes.
func showOne(s reduce.Snapshot, h ViewHeader, root reduce.Record, stale StaleCheck) *ShowAnswer {
	v, notes := newDetailer(s).detail(root)
	a := &ShowAnswer{ViewHeader: h, Records: []Detail{v}, Attention: append([]Attention{}, notes...)}
	if v.Task != nil && v.Task.Status == reduce.StatusBlocked {
		a.Attention = append(a.Attention, owedBy(v.Ref, v.Label, v.Task.Reasons)...)
	}
	if v.Claim != nil {
		runs := []RunDetail{}
		for _, id := range v.Claim.Observations {
			if inv, ok := s.Invocation(reduce.InvocationKey{Project: s.Project(), InvocationID: id}); ok {
				runs = append(runs, runDetail(s, inv))
			}
		}
		a.Runs = &runs
	}
	if stale != nil {
		a.Stale = staleOf(s, stale, map[model.ID]bool{root.Key.ID: true})
	}
	return a
}

func staleOf(s reduce.Snapshot, check StaleCheck, only map[model.ID]bool) *StaleSection {
	out := &StaleSection{BlindSpot: staleBlindSpot, Claims: []StaleClaim{}}
	for _, c := range check(s) {
		if only == nil || only[c.Claim.RecordID] {
			out.Claims = append(out.Claims, c)
		}
	}
	return out
}

// showAll is bare show, or bare show restricted to kind when kind is set.
func showAll(s reduce.Snapshot, h ViewHeader, kind model.Kind, stale StaleCheck) *ShowAnswer {
	d := newDetailer(s)
	a := &ShowAnswer{ViewHeader: h, Records: []Detail{}, Attention: []Attention{}}
	sum := &Summary{Kind: "all"}
	if kind != "" {
		sum.Kind = string(kind)
	}
	shows := func(k model.Kind) bool { return kind == "" || kind == k }
	tasks, claims, decisions, instruments := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	owed, owedNotes, instrumentNotes := &OwedCounts{}, []Attention{}, []Attention{}
	observed := map[model.ID]bool{}
	for _, fact := range currentOfKind(s, kind) {
		if !shows(fact.Kind) {
			continue
		}
		v, notes := d.detail(fact)
		instrumentNotes = append(instrumentNotes, notes...)
		a.Records = append(a.Records, v)
		switch {
		case v.Task != nil:
			tasks[string(v.Task.Status)]++
			switch v.Task.Status {
			case reduce.StatusInFlight:
				owed.InFlight++
			case reduce.StatusReady:
				owed.Ready++
			case reduce.StatusBlocked:
				owedNotes = append(owedNotes, owedBy(v.Ref, v.Label, v.Task.Reasons)...)
				if hasReasonKind(v.Task.Reasons, reduce.ReasonAwaitingAcceptance) {
					owed.AwaitingAcceptance++
				} else {
					owed.Blocked++
				}
			}
		case v.Claim != nil:
			claims[string(v.Claim.Status)]++
			for _, id := range v.Claim.Observations {
				observed[id] = true
			}
		case v.Decision != nil:
			decisions[string(v.Decision.Status)]++
			if v.Decision.Status == reduce.StatusOpen {
				owed.OpenDecisions++
			}
		case v.Instrument != nil:
			instruments["validation "+v.Instrument.Validation.State]++
			instruments["trust "+string(v.Instrument.Trust)]++
		}
	}
	sort.SliceStable(a.Records, func(i, j int) bool {
		return rank(a.Records[i].Fact.Kind) < rank(a.Records[j].Fact.Kind)
	})
	runNotes := []Attention{}
	if kind == "" || kind == model.Task || kind == model.Claim {
		runs := []RunDetail{}
		for _, inv := range s.Invocations() {
			if kind == model.Claim && !observed[inv.Key.InvocationID] {
				continue
			}
			run := runDetail(s, inv)
			runs = append(runs, run)
			if note, outside := runAttention(run.RunView); outside && kind != model.Claim {
				runNotes = append(runNotes, note)
			}
		}
		a.Runs = &runs
		counts := &RunCounts{Total: len(runs)}
		for _, run := range runs {
			if run.Sealed {
				counts.Sealed++
			} else {
				counts.Unsealed++
			}
		}
		sum.Runs = counts
	}
	a.Attention = append(append(append(a.Attention, instrumentNotes...), runNotes...), owedNotes...)
	sum.Records, sum.Attention = len(a.Records), len(a.Attention)
	if shows(model.Task) {
		sum.Tasks = &tasks
	}
	if shows(model.Claim) {
		sum.Claims = &claims
	}
	if shows(model.Decision) {
		sum.Decisions = &decisions
	}
	if shows(model.Instrument) {
		sum.Instruments = &instruments
	}
	if kind == "" {
		sum.Owed = owed
	}
	a.Summary = sum
	if stale != nil {
		a.Stale = staleOf(s, stale, nil)
	}
	return a
}

// currentOfKind is currentRecords, read through the instrument list when only
// instruments are asked for, so it does not copy every record to keep a few.
func currentOfKind(s reduce.Snapshot, kind model.Kind) []reduce.Record {
	if kind != model.Instrument {
		return currentRecords(s)
	}
	out := []reduce.Record{}
	for _, p := range s.Instruments() {
		if fact, ok := s.Record(asRef(p.Instrument)); ok {
			out = append(out, fact)
		}
	}
	return out
}

func rank(k model.Kind) int {
	if r, ok := kindOrder[k]; ok {
		return r
	}
	return len(kindOrder)
}
