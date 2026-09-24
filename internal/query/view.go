package query

// The four views of the command surface (COMMAND-SPEC §3, R19): todo,
// continue, show and history. This file holds their request, the answer
// header every view carries, request checks and routing. Each view's
// selection lives in its own view_*.go file; the record detail they share is
// detail.go.

import (
	"fmt"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

type ViewRequest struct {
	View         string                   // todo, continue, show or history
	ID           model.ID                 // continue (required), show and history (optional)
	Kind         string                   // show without ID only: task, claim, decision or instrument
	Limit        int                      // todo and continue: the optional-result cap; 0 means none
	Observed     *Observation             // continue only: the caller's fresh workspace observation
	Stale        *StaleGit                // show only, on request: the caller runs git, so never by default
	SelfAdmitted model.SelfAdmissionState // history without ID only
}

// ViewHeader opens every view answer: which view, which project, and the
// watermark of the one ledger prefix the whole answer was read from.
type ViewHeader struct {
	View      string          `json:"view"`
	Project   model.ProjectID `json:"project"`
	Watermark Watermark       `json:"watermark"`
	Result    string          `json:"result"`
	Reason    string          `json:"reason,omitempty"`
}

// ViewAnswer is one of *TodoAnswer, *ContinueAnswer, *ShowAnswer or
// *HistoryAnswer. JSON and the brief both render the concrete answer.
type ViewAnswer interface{ Header() ViewHeader }

func (h ViewHeader) Header() ViewHeader { return h }

var viewKinds = map[string]model.Kind{"task": model.Task, "claim": model.Claim, "decision": model.Decision, "instrument": model.Instrument}

// ReadView selects the immutable ledger prefix once and answers one view.
func ReadView(project store.Project, request ViewRequest) (ViewAnswer, error) {
	if err := CheckView(request); err != nil {
		return nil, err
	}
	source, err := store.Load(project)
	if err != nil {
		return nil, err
	}
	return view(project, request, source)
}

// ReadViewFrom answers from one already selected prefix.
func ReadViewFrom(project store.Project, request ViewRequest, source Source) (ViewAnswer, error) {
	if err := CheckView(request); err != nil {
		return nil, err
	}
	return view(project, request, source)
}

// CheckView refuses a request no view can answer, before any ledger is read.
func CheckView(r ViewRequest) error {
	switch {
	case r.View != "todo" && r.View != "continue" && r.View != "show" && r.View != "history":
		return fmt.Errorf("unknown view %q: todo, continue, show or history", r.View)
	case r.ID != "" && !model.ValidID(r.ID):
		return fmt.Errorf("%q is not a record ULID", r.ID)
	case r.ID != "" && r.View == "todo":
		return fmt.Errorf("todo takes no record ULID")
	case r.ID == "" && r.View == "continue":
		return fmt.Errorf("continue requires a record ULID")
	case r.Kind != "" && (r.View != "show" || r.ID != ""):
		return fmt.Errorf("--kind applies only to show without a record ULID")
	case r.Kind != "" && viewKinds[r.Kind] == "":
		return fmt.Errorf("--kind must be task, claim, decision or instrument")
	case r.Limit < 0:
		return fmt.Errorf("limit must be positive")
	case r.Limit > 0 && r.View != "todo" && r.View != "continue":
		return fmt.Errorf("limit applies only to the optional results of todo and continue")
	case r.Observed != nil && r.View != "continue":
		return fmt.Errorf("a workspace observation belongs only to continue")
	case r.Stale != nil && r.View != "show":
		return fmt.Errorf("the stale-claims check belongs only to show")
	case r.SelfAdmitted != "" && r.View != "history":
		return fmt.Errorf("the self-admitted filter belongs only to history")
	}
	return checkSelfAdmitted(r)
}

// checkSelfAdmitted holds the history filter's own rules.
func checkSelfAdmitted(r ViewRequest) error {
	if r.SelfAdmitted == "" {
		return nil
	}
	if r.View != "history" || r.ID != "" {
		return fmt.Errorf("self-admitted filter requires history without a record ULID")
	}
	if r.SelfAdmitted != model.SelfAdmissionTrue && r.SelfAdmitted != model.SelfAdmissionFalse && r.SelfAdmitted != model.SelfAdmissionUnknown {
		return fmt.Errorf("self-admitted must be true, false, or unknown")
	}
	return nil
}

func watermarkOf(s reduce.Snapshot) Watermark {
	w := s.Watermark()
	out := Watermark{Sequence: w.Sequence, Bundles: w.Bundles, Events: w.Events,
		Head: Unknown{State: "UNKNOWN", Reason: "no admitted bundle in this prefix"}}
	if w.Bundles > 0 {
		out.Head = Head{CommandID: w.CommandID, RecordedAt: w.RecordedAt}
	}
	return out
}

func view(project store.Project, r ViewRequest, source Source) (ViewAnswer, error) {
	s := source.Snapshot()
	h := ViewHeader{View: r.View, Project: project.ID, Watermark: watermarkOf(s), Result: "KNOWN"}
	var root reduce.Record
	if r.ID != "" {
		var ok bool
		if root, ok = s.Current(reduce.Ident{Project: project.ID, ID: r.ID}); !ok {
			h.Result, h.Reason = "UNKNOWN", fmt.Sprintf("no admitted record %s in this project at this watermark", r.ID)
		}
	}
	switch r.View {
	case "todo":
		return todoView(project, s, h, r.Limit)
	case "continue":
		if h.Result == "UNKNOWN" {
			return &ContinueAnswer{ViewHeader: h, Records: map[string]Detail{}, Attention: []Attention{}}, nil
		}
		return continueView(s, h, root, r), nil
	case "show":
		if h.Result == "UNKNOWN" {
			return &ShowAnswer{ViewHeader: h, Records: []Detail{}, Attention: []Attention{}}, nil
		}
		if r.ID != "" {
			return showOne(s, h, root, r.Stale), nil
		}
		return showAll(s, h, viewKinds[r.Kind], r.Stale), nil
	}
	return historyView(project, source, h, r)
}

// HistoryAnswer is the history view, unchanged from the history read: ledger
// events in order and, without an ID, every per-packet review.
type HistoryAnswer struct {
	ViewHeader
	Events  []Event  `json:"events"`
	Reviews []Review `json:"reviews"`
}

func historyView(project store.Project, source Source, h ViewHeader, r ViewRequest) (ViewAnswer, error) {
	a := &HistoryAnswer{ViewHeader: h, Events: []Event{}, Reviews: []Review{}}
	if h.Result == "UNKNOWN" {
		return a, nil
	}
	snapshot := source.Snapshot()
	// Review packet refs do not identify records. Audit the complete prefix
	// independently of intake, without assigning bundle siblings to a packet.
	if r.ID == "" {
		for _, review := range snapshot.Reviews() {
			if r.SelfAdmitted == "" || review.SelfAdmission == r.SelfAdmitted {
				a.Reviews = append(a.Reviews, describeReview(review))
			}
		}
	}
	if r.SelfAdmitted != "" {
		return a, nil
	}
	selected := historyOrigins(snapshot, reduce.Ident{Project: project.ID, ID: r.ID})
	prefix, err := source.Bundles()
	if err != nil {
		return nil, err
	}
	for _, bundle := range prefix {
		for i, event := range bundle.Events {
			origin := reduce.Origin{Sequence: bundle.Sequence, EventIndex: i}
			if r.ID == "" || selected[origin] {
				a.Events = append(a.Events, Event{origin, bundle.CommandID, bundle.Admitter, bundle.Packets, event, snapshot.EventAuthor(origin)})
			}
		}
	}
	return a, nil
}
