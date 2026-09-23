// Package query selects admitted facts before either output format renders them.
package query

import (
	"fmt"
	"sort"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

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

// pending projects the visible intake against this answer's reviews. Every
// packet the answer presents (pending, or reviewed but not accepted) is fully
// verified, blobs included. A packet the ledger accepted is dropped from the
// answer; a read only checks that its packet.json still hashes to the digest
// its review recorded (store.ReviewedPacketDigest) and leaves its blobs to
// admission, which verified them before the review existed.
func pending(project store.Project, s reduce.Snapshot) ([]Packet, error) {
	ids, err := store.IntakeIDs(project)
	if err != nil {
		return nil, err
	}
	byID := map[model.ID]Packet{}
	for _, id := range ids {
		byID[id] = Packet{CommandID: id, Disposition: "pending"}
	}
	// Review events are the admitted facts, even without envelope packet refs
	// or a local copy of the intake bytes. They are met in ledger order, so the
	// first mismatch reported is the one a walk of the prefix would report.
	for _, review := range s.ReviewsInLedgerOrder() {
		ref := review.Packet
		p, present := byID[ref.CommandID]
		if present && p.Unavailable == nil {
			digest, err := store.ReviewedPacketDigest(project, ref.CommandID)
			if err != nil {
				return nil, err
			}
			if digest != review.Packet.Digest {
				return nil, fmt.Errorf("intake %s no longer matches its reviewed bytes", ref.CommandID)
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
	presented := []model.ID{}
	for _, packet := range out {
		if packet.Unavailable == nil {
			presented = append(presented, packet.CommandID)
		}
	}
	if len(presented) == 0 {
		return out, nil
	}
	verified, err := store.ReadVerifiedIntake(project, presented)
	if err != nil {
		return nil, err
	}
	next := 0
	for i := range out {
		if out[i].Unavailable != nil {
			continue
		}
		out[i].Packet = &verified[next].Packet
		next++
	}
	return out, nil
}
