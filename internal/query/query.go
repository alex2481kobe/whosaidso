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
	Command      string // show, history, intake pending, or a preset in presets.go
	ID           model.ID
	SelfAdmitted model.SelfAdmissionState // empty means no filter; history only, without ID
	Limit        int                      // optional-result cap for context, continue and todo; 0 means none
	Observed     *Observation             // continue only: the caller's fresh workspace observation
	Disposal     *DisposalTarget          // disposal-loss only: the artifact a disposal would name
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
	Preset    *Preset         `json:"preset,omitempty"` // only the read presets fill this
}

// Review exposes the projected per-packet fact, including legacy UNKNOWN.
// The embedded review preserves its identity, disposition, reason and origin.
// This SelfAdmission shadows the embedded one under the same key, so exactly
// one self_admission is exported: this rendered state.
type Review struct {
	reduce.Review
	SelfAdmission string `json:"self_admission"`
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
	Author         reduce.PacketAuthor          `json:"author"` // who wrote the packet that carried Fact
	Task           *Task                        `json:"task,omitempty"`
	Claim          *reduce.ClaimProjection      `json:"claim,omitempty"`
	Decision       *reduce.DecisionProjection   `json:"decision,omitempty"`
	Instrument     *reduce.InstrumentProjection `json:"instrument,omitempty"`
	CurrentSupport reduce.Truth                 `json:"current_support,omitempty"`
	Supersessions  []reduce.Supersession        `json:"supersessions"` // either side; the superseded record stays shown
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
	Origin    reduce.Origin       `json:"origin"`
	CommandID model.ID            `json:"command_id"`
	Admitter  model.Actor         `json:"admitter"`
	Packets   []model.PacketRef   `json:"packets"`
	Event     model.Event         `json:"event"`
	Author    reduce.PacketAuthor `json:"author"` // who wrote the packet that carried Event
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
// answer's watermark, never a later ledger read. The only generated file read
// is the snapshot cache, and only after every byte of the prefix it was folded
// from is checked (store.Load); deleting it changes no answer.
func Read(project store.Project, request Request) (Answer, error) {
	if err := checkRequest(request); err != nil {
		return Answer{}, err
	}
	source, err := store.Load(project)
	if err != nil {
		return Answer{}, err
	}
	return answer(project, request, source)
}

// ReadFrom answers from one already selected prefix. Every view reads that one
// Source, so no answer can combine two watermarks.
func ReadFrom(project store.Project, request Request, source Source) (Answer, error) {
	if err := checkRequest(request); err != nil {
		return Answer{}, err
	}
	return answer(project, request, source)
}

func checkRequest(request Request) error {
	if request.SelfAdmitted != "" {
		if request.Command != "history" || request.ID != "" {
			return fmt.Errorf("self-admitted filter requires history without a record ULID")
		}
		if request.SelfAdmitted != model.SelfAdmissionTrue && request.SelfAdmitted != model.SelfAdmissionFalse && request.SelfAdmitted != model.SelfAdmissionUnknown {
			return fmt.Errorf("self-admitted must be true, false, or unknown")
		}
	}
	if request.Command != "show" && request.Command != "history" && request.Command != "intake pending" && !presetCommands[request.Command] {
		return fmt.Errorf("unknown read command %q", request.Command)
	}
	if request.ID != "" && (!model.ValidID(request.ID) || request.Command != "show" && request.Command != "history" && !idPresets[request.Command]) {
		return fmt.Errorf("only show, history, context and continue accept a record ULID")
	}
	return checkPresetRequest(request)
}

func answer(project store.Project, request Request, source Source) (Answer, error) {
	var err error
	snapshot := source.Snapshot()
	w := snapshot.Watermark()
	a := Answer{Command: request.Command, Project: project.ID, Result: "KNOWN",
		Watermark: Watermark{Sequence: w.Sequence, Bundles: w.Bundles, Events: w.Events,
			Head: Unknown{State: "UNKNOWN", Reason: "no admitted bundle in this prefix"}},
		Records: []Record{}, History: []Event{}, Intake: []Packet{}, Reviews: []Review{}}
	if w.Bundles > 0 {
		a.Watermark.Head = Head{CommandID: w.CommandID, RecordedAt: w.RecordedAt}
	}
	id := reduce.Ident{Project: project.ID, ID: request.ID}
	var current reduce.Record
	if request.ID != "" {
		var ok bool
		if current, ok = snapshot.Current(id); !ok {
			a.Result, a.Reason = "UNKNOWN", fmt.Sprintf("no admitted record %s in this project at this watermark", request.ID)
			return a, nil
		}
	}
	switch request.Command {
	case "show":
		// One record is its current revision, read directly; copying and
		// sorting every record to keep one would answer the same, slower.
		if request.ID != "" {
			a.Records = append(a.Records, describe(snapshot, current))
			break
		}
		for _, fact := range snapshot.Records() {
			who := reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}
			revision, _ := snapshot.CurrentRevision(who)
			if fact.Key.Revision != revision {
				continue
			}
			r := describe(snapshot, fact)
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
		prefix, err := source.Bundles()
		if err != nil {
			return Answer{}, err
		}
		for _, bundle := range prefix {
			for i, event := range bundle.Events {
				origin := reduce.Origin{Sequence: bundle.Sequence, EventIndex: i}
				if request.ID == "" || selected[origin] {
					a.History = append(a.History, Event{origin, bundle.CommandID, bundle.Admitter, bundle.Packets, event, snapshot.EventAuthor(origin)})
				}
			}
		}
	case "intake pending":
		a.Intake, err = pending(project, snapshot)
	default:
		err = preset(project, snapshot, request, &a)
	}
	return a, err
}

func pending(project store.Project, s reduce.Snapshot) ([]Packet, error) {
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
	// or a local copy of the intake bytes. They are met in ledger order, so the
	// first mismatch reported is the one a walk of the prefix would report.
	for _, review := range s.ReviewsInLedgerOrder() {
		ref := review.Packet
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
	out := make([]Packet, 0, len(byID))
	for _, packet := range byID {
		out = append(out, packet)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommandID < out[j].CommandID })
	return out, nil
}
