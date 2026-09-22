// Package query selects admitted facts before either output format renders them.
package query

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

type Request struct {
	Command      string // show, history, task todo, or intake pending
	ID           model.ID
	SelfAdmitted model.SelfAdmissionState // empty means no filter; history only, without ID
}

// Answer is the complete read result shared by text and JSON. UNKNOWN is a
// successful answer for an absent record; corrupt storage is instead an error.
type Answer struct {
	Command   string          `json:"command"`
	Project   model.ProjectID `json:"project"`
	Watermark Watermark       `json:"watermark"`
	Result    string          `json:"result"`
	Reason    string          `json:"reason,omitempty"`
	Records   []Record        `json:"records"`
	History   []Event         `json:"history"`
	Intake    []Packet        `json:"intake"`
	Reviews   []Review        `json:"reviews"`
}

// Review exposes the projected per-packet fact, including legacy UNKNOWN.
// The embedded review preserves its identity, disposition, reason and origin.
type Review struct {
	reduce.Review
	SelfAdmission string `json:"SelfAdmission"`
}

func describeReview(review reduce.Review) Review {
	state := string(review.SelfAdmission)
	if review.SelfAdmission == model.SelfAdmissionUnknown {
		state = "UNKNOWN"
	}
	return Review{Review: review, SelfAdmission: state}
}

// A zero-bundle prefix has known counts but no head, not a year-one timestamp.
type Watermark struct {
	Sequence uint64 `json:"sequence"`
	Bundles  int    `json:"bundles"`
	Events   int    `json:"events"`
	Head     any    `json:"head"` // Head or Unknown
}
type Head struct {
	CommandID  model.ID  `json:"command_id"`
	RecordedAt time.Time `json:"recorded_at"`
}
type Unknown struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type Record struct {
	Fact           reduce.Record                `json:"fact"`
	Task           *Task                        `json:"task,omitempty"`
	Claim          *reduce.ClaimProjection      `json:"claim,omitempty"`
	Decision       *reduce.DecisionProjection   `json:"decision,omitempty"`
	Instrument     *reduce.InstrumentProjection `json:"instrument,omitempty"`
	CurrentSupport reduce.Truth                 `json:"current_support,omitempty"`
	Sources        []reduce.Source              `json:"sources"`
}

type Task struct {
	Revision          model.Revision              `json:"revision"`
	Status            reduce.TaskStatus           `json:"status"`
	Outcome           string                      `json:"outcome"`
	Closure           *reduce.Closure             `json:"closure"`
	Attempts          []reduce.Attempt            `json:"attempts"`
	AttemptHolders    []Holder                    `json:"attempt_holders"`
	Blockers          []reduce.Blocker            `json:"blockers"`
	Prerequisites     []reduce.PrerequisiteResult `json:"prerequisites"`
	Reasons           []reduce.BlockedReason      `json:"reasons"`
	WaitingActors     []model.Actor               `json:"waiting_actors"`
	ExpectedNextActor any                         `json:"expected_next_actor"` // Actor or Unknown
	CommitsDenied     bool                        `json:"commits_denied"`
}
type Holder struct {
	Attempt reduce.AttemptKey `json:"attempt"`
	Actor   any               `json:"actor"` // Actor or Unknown
}
type Event struct {
	Origin    reduce.Origin     `json:"origin"`
	CommandID model.ID          `json:"command_id"`
	Admitter  model.Actor       `json:"admitter"`
	Packets   []model.PacketRef `json:"packets"`
	Event     model.Event       `json:"event"`
}
type Packet struct {
	CommandID   model.ID      `json:"command_id"`
	Packet      *model.Packet `json:"packet"`
	Unavailable *Unknown      `json:"unavailable,omitempty"`
	Disposition string        `json:"disposition"`
	Review      *Review       `json:"review"`
}

// Read selects the immutable ledger prefix once. Intake is a separate visible
// inventory, read afterwards; its dispositions are always relative to this
// answer's watermark, never a later ledger read. No generated files are read.
func Read(project store.Project, request Request) (Answer, error) {
	if request.SelfAdmitted != "" {
		if request.Command != "history" || request.ID != "" {
			return Answer{}, fmt.Errorf("self-admitted filter requires history without a record ULID")
		}
		if request.SelfAdmitted != model.SelfAdmissionTrue && request.SelfAdmitted != model.SelfAdmissionFalse && request.SelfAdmitted != model.SelfAdmissionUnknown {
			return Answer{}, fmt.Errorf("self-admitted must be true, false, or unknown")
		}
	}
	if request.Command != "show" && request.Command != "history" && request.Command != "task todo" && request.Command != "intake pending" {
		return Answer{}, fmt.Errorf("unknown read command %q", request.Command)
	}
	if request.ID != "" && (!model.ValidID(request.ID) || request.Command != "show" && request.Command != "history") {
		return Answer{}, fmt.Errorf("only show and history accept a record ULID")
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		return Answer{}, err
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		return Answer{}, err
	}
	w := snapshot.Watermark()
	a := Answer{Command: request.Command, Project: project.ID, Result: "KNOWN",
		Watermark: Watermark{Sequence: w.Sequence, Bundles: w.Bundles, Events: w.Events,
			Head: Unknown{State: "UNKNOWN", Reason: "no admitted bundle in this prefix"}},
		Records: []Record{}, History: []Event{}, Intake: []Packet{}, Reviews: []Review{}}
	if w.Bundles > 0 {
		a.Watermark.Head = Head{CommandID: w.CommandID, RecordedAt: w.RecordedAt}
	}
	id := reduce.Ident{Project: project.ID, ID: request.ID}
	if request.ID != "" {
		if _, ok := snapshot.Current(id); !ok {
			a.Result, a.Reason = "UNKNOWN", fmt.Sprintf("no admitted record %s in this project at this watermark", request.ID)
			return a, nil
		}
	}
	switch request.Command {
	case "show", "task todo":
		for _, fact := range snapshot.Records() {
			who := reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}
			revision, _ := snapshot.CurrentRevision(who)
			if fact.Key.Revision != revision || request.ID != "" && who != id {
				continue
			}
			r := describe(snapshot, fact)
			if request.Command == "task todo" && (r.Task == nil || r.Task.Status == reduce.StatusClosed) {
				continue
			}
			a.Records = append(a.Records, r)
		}
	case "history":
		// Review packet refs do not identify records. Audit the complete prefix
		// independently of intake, without assigning bundle siblings to a packet.
		if request.ID == "" {
			for _, review := range snapshot.Reviews() {
				if request.SelfAdmitted == "" || review.SelfAdmission == request.SelfAdmitted {
					a.Reviews = append(a.Reviews, describeReview(review))
				}
			}
		}
		if request.SelfAdmitted != "" {
			break
		}
		selected := historyOrigins(snapshot, id)
		for _, bundle := range prefix {
			for i, event := range bundle.Events {
				origin := reduce.Origin{Sequence: bundle.Sequence, EventIndex: i}
				if request.ID == "" || selected[origin] {
					a.History = append(a.History, Event{origin, bundle.CommandID, bundle.Admitter, bundle.Packets, event})
				}
			}
		}
	case "intake pending":
		a.Intake, err = pending(project, snapshot, prefix)
	}
	return a, err
}

func pending(project store.Project, s reduce.Snapshot, prefix []model.Bundle) ([]Packet, error) {
	packets, err := store.ReadIntake(project, nil)
	if err != nil {
		return nil, err
	}
	byID := map[model.ID]Packet{}
	for i := range packets {
		p := &packets[i]
		byID[p.CommandID] = Packet{CommandID: p.CommandID, Packet: p, Disposition: "pending"}
	}
	// Review events are the admitted facts, even without envelope packet refs
	// or a local copy of the intake bytes.
	for _, bundle := range prefix {
		var refs []model.PacketRef
		for _, event := range bundle.Events {
			if event.Type != "review.admit" {
				continue
			}
			typed, err := model.DecodeEvent(event)
			if err != nil {
				return nil, err
			}
			refs = append(refs, typed.(*model.ReviewAdmit).Packets...)
		}
		for _, ref := range refs {
			review, ok := s.Review(reduce.ReviewKey{Project: project.ID, CommandID: ref.CommandID})
			if !ok {
				continue
			}
			p, present := byID[ref.CommandID]
			if present && p.Packet != nil {
				dir, err := store.IntakeDir(project)
				if err != nil {
					return nil, err
				}
				data, err := os.ReadFile(filepath.Join(dir, string(ref.CommandID), "packet.json"))
				if err != nil || model.HashBytes(data) != review.Packet.Digest {
					return nil, fmt.Errorf("intake %s no longer matches its reviewed bytes: %v", ref.CommandID, err)
				}
			}
			if review.Outcome == "accepted" {
				delete(byID, ref.CommandID)
				continue
			}
			if !present {
				p = Packet{CommandID: ref.CommandID, Unavailable: &Unknown{"UNKNOWN", "reviewed packet is absent from local intake"}}
			}
			projected := describeReview(review)
			p.Disposition, p.Review = review.Outcome, &projected
			byID[ref.CommandID] = p
		}
	}
	out := make([]Packet, 0, len(byID))
	for _, packet := range byID {
		out = append(out, packet)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommandID < out[j].CommandID })
	return out, nil
}

func actor(a model.Actor) any {
	if a.ID == "" {
		return Unknown{State: "UNKNOWN", Reason: a.UnknownReason}
	}
	return a
}

func describe(s reduce.Snapshot, fact reduce.Record) Record {
	id := reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}
	r := Record{Fact: fact, Sources: []reduce.Source{}}
	if p, ok := s.Task(id); ok {
		outcome := string(p.Outcome)
		if outcome == "" {
			outcome = "UNKNOWN"
		}
		r.Task = &Task{Revision: p.Task.Revision, Status: p.Status, Outcome: outcome, Closure: p.Closure,
			Attempts: p.Attempts, AttemptHolders: []Holder{}, Blockers: p.Blockers, Prerequisites: p.Prerequisites,
			Reasons: p.Reasons, WaitingActors: p.WaitingActors, ExpectedNextActor: actor(p.NextActor), CommitsDenied: p.CommitsDenied}
		for _, attempt := range p.Attempts {
			if attempt.Live() {
				r.Task.AttemptHolders = append(r.Task.AttemptHolders, Holder{attempt.Key, actor(attempt.Actor)})
			}
		}
	}
	ref := model.RecordRef{Project: fact.Key.Project, RecordID: fact.Key.ID, Revision: fact.Key.Revision}
	// No evidence bytes or real-world scope were checked by this read slice.
	support, _ := s.Support(ref, reduce.SupportContext{EvidenceAvailable: reduce.TruthUnknown, ScopeApplicable: reduce.TruthUnknown})
	if p, ok := s.Claim(id); ok {
		p.Support = support
		r.Claim, r.CurrentSupport = &p, support.Current()
	}
	if p, ok := s.Decision(id); ok {
		p.Support = support
		r.Decision, r.CurrentSupport = &p, support.Current()
	}
	if p, ok := s.Instrument(id); ok {
		p.Support = support
		r.Instrument, r.CurrentSupport = &p, support.Current()
	}
	for _, source := range s.Sources() {
		for _, target := range source.Intake.Referents {
			if target.Project == id.Project && target.RecordID == id.ID {
				r.Sources = append(r.Sources, source)
				break
			}
		}
	}
	return r
}

// History is all revisions plus events that explicitly reference those
// revisions, including receipts naming a claim through their criterion reference
// or owned by this task's attempts.
// It is not a recursive traversal of neighboring records or inferred sources.
func historyOrigins(s reduce.Snapshot, id reduce.Ident) map[reduce.Origin]bool {
	origins := map[reduce.Origin]bool{}
	for _, record := range s.Records() {
		if record.Key.Project != id.Project || record.Key.ID != id.ID {
			continue
		}
		origins[record.Origin] = true
		ref := model.RecordRef{Project: id.Project, RecordID: id.ID, Revision: record.Key.Revision}
		for _, edge := range s.RecordReferrers(ref) {
			origins[edge.Origin] = true
		}
	}
	for _, invocation := range s.Invocations() {
		criterion := invocation.Start.CriterionRef.Value
		namesClaim := criterion != nil && criterion.Claim.Project == id.Project && criterion.Claim.RecordID == id.ID
		if namesClaim || invocation.Attempt.Project == id.Project && invocation.Attempt.Task == id.ID {
			origins[invocation.Started] = true
			if invocation.Sealed != nil {
				origins[*invocation.Sealed] = true
			}
		}
	}
	return origins
}
