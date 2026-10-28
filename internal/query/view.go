package query

// The four views of the command surface: todo,
// continue, show and history. This file holds their request, the answer
// header every view carries, request checks and routing. Each view's
// selection lives in its own view_*.go file; the record detail they share is
// detail.go.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type ViewRequest struct {
	Top          bool                     // todo: only top-level open tasks
	Under        model.ID                 // todo: direct open prerequisites of this task
	Actor        string                   // todo only: task next actor or live holder
	View         string                   // todo, continue, show, history or packets
	ID           model.ID                 // continue (required), show and history (optional)
	Kind         string                   // show without ID only: task, claim, decision or instrument
	Limit        int                      // todo and continue: the optional-result cap; 0 means none
	Observed     *Observation             // continue only: the caller's fresh workspace observation
	Stale        *StaleGit                // show only, on request: the caller runs git, so never by default
	SelfAdmitted model.SelfAdmissionState // history without ID only
	Now          time.Time                // todo only: the caller's clock, to judge claims stale; zero judges none
	Brief        bool                     // todo only: the caller renders the brief, so the answer may omit what no brief line reads (each task's sources)
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

// ViewAnswer is a concrete read answer. JSON and the brief render it.
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
	case (r.Top || r.Under != "") && r.View != "todo":
		return fmt.Errorf("--top and --under belong only to todo")
	case r.Top && r.Under != "":
		return fmt.Errorf("--top and --under cannot be combined")
	case r.Under != "" && !model.ValidID(r.Under):
		return fmt.Errorf("--under requires a task ULID")
	case r.Actor != "" && r.View != "todo":
		return fmt.Errorf("the actor filter belongs only to todo")
	case r.View != "todo" && r.View != "continue" && r.View != "show" && r.View != "history" && r.View != "packets":
		return fmt.Errorf("unknown view %q: todo, continue, show, history or packets", r.View)
	case r.ID != "" && !model.ValidID(r.ID):
		if r.View == "packets" {
			return fmt.Errorf("%q is not a packet command ULID", r.ID)
		}
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
	case !r.Now.IsZero() && r.View != "todo":
		return fmt.Errorf("the reading time belongs only to todo")
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
	if r.View == "packets" {
		return packetsView(project, s, h, r.ID)
	}
	var root reduce.Record
	var intake *reduce.Source // r.ID names an admitted source, not a record
	if r.ID != "" {
		var ok bool
		id := reduce.Ident{Project: project.ID, ID: r.ID}
		if root, ok = s.Current(id); !ok {
			if src, found := sourceOf(s.Sources(), id); found {
				intake = &src
			} else {
				h.Result, h.Reason = "UNKNOWN", fmt.Sprintf("no admitted record %s in this project at this watermark", r.ID)
			}
		}
	}
	switch r.View {
	case "todo":
		if r.Top || r.Under != "" {
			return taskList(s, h, r), nil
		}
		return todoView(project, s, h, r.Limit, r.Now, r.Actor, r.Brief)
	case "continue":
		if intake != nil {
			h.Result, h.Reason = "UNKNOWN", fmt.Sprintf("%s is an admitted source, not a record to resume: show %s or history %s reads it", r.ID, r.ID, r.ID)
		}
		if h.Result == "UNKNOWN" {
			return &ContinueAnswer{ViewHeader: h, Records: map[string]Detail{}, Attention: []Attention{}}, nil
		}
		return continueView(s, h, root, r), nil
	case "show":
		if h.Result == "UNKNOWN" {
			return &ShowAnswer{ViewHeader: h, Records: []Detail{}, Attention: []Attention{}}, nil
		}
		if intake != nil {
			return &ShowAnswer{ViewHeader: h, Records: []Detail{}, Sources: &[]SourceDetail{sourceDetail(s, *intake)}, Attention: []Attention{}}, nil
		}
		if r.ID != "" {
			return showOne(s, h, root, r.Stale), nil
		}
		return showAll(s, h, viewKinds[r.Kind], r.Stale), nil
	}
	return historyView(project, source, h, r)
}

// HistoryAnswer is the history view: ledger events in order, each amending
// event with what it changed, and, without an ID, every per-packet review.
type HistoryAnswer struct {
	ViewHeader
	Events  []Event  `json:"events"`
	Reviews []Review `json:"reviews"`
}

func historyView(project store.Project, source Source, h ViewHeader, r ViewRequest) (ViewAnswer, error) {
	a := &HistoryAnswer{ViewHeader: h, Events: []Event{}, Reviews: []Review{}}
	snapshot := source.Snapshot()
	// A record no applied bundle holds may still be what set-aside bundles
	// were about; nothing set aside is hidden, so their events still list.
	if h.Result == "UNKNOWN" && len(snapshot.SetAside()) == 0 {
		return a, nil
	}
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
	about := reduce.Ident{Project: project.ID, ID: r.ID}
	selected := map[reduce.Origin]bool{}
	if h.Result != "UNKNOWN" {
		selected = historyOrigins(snapshot, about)
	}
	amended := snapshot.Amended()
	prefix, err := source.Bundles()
	if err != nil {
		return nil, err
	}
	for position, bundle := range prefix {
		var aside *reduce.SetAside
		if v, ok := snapshot.SetAsideBundle(bundle.Ref()); ok {
			aside = &v
		}
		for i, event := range bundle.Events {
			// The prefix is in canonical order, so its index is the position
			// every origin in the snapshot carries.
			origin := reduce.Origin{Sequence: uint64(position) + 1, EventIndex: i}
			if r.ID != "" && !selected[origin] && (aside == nil || !touches(snapshot, event, about)) {
				continue
			}
			row := Event{Origin: origin, CommandID: bundle.CommandID, Admitter: bundle.Admitter, Packets: bundle.Packets,
				Event: event, Author: eventAuthor(snapshot, origin, event), SetAside: aside}
			if record, ok := amended[origin]; ok {
				if amendment, ok := amendmentOf(snapshot, record); ok {
					row.Amendment = &amendment
				}
			}
			a.Events = append(a.Events, row)
		}
	}
	return a, nil
}

// eventAuthor answers who wrote one event. A packet's event was written by
// the packet's author. A review.admit is carried by no packet: the admission
// constructs it, so its author is the reviewing actor it records, and it names
// no packet.
func eventAuthor(s reduce.Snapshot, origin reduce.Origin, event model.Event) reduce.PacketAuthor {
	if event.Type != (*model.ReviewAdmit)(nil).EventType() {
		return s.EventAuthor(origin)
	}
	var review struct {
		Actor model.Actor `json:"actor"`
	}
	if err := json.Unmarshal(event.Data, &review); err != nil {
		return reduce.PacketAuthor{Author: model.Actor{UnknownReason: "this review.admit's actor could not be read: " + err.Error()}}
	}
	return reduce.PacketAuthor{Author: review.Actor}
}
