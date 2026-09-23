// Package write joins immutable capture to canonical state through one admission gate.
package write

// Admission request identity, packet verification, and the transaction live here.
// Artifact resolution and durable blob preservation do not.
// This file stays near 200 lines to keep the admission transaction whole.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// AdmitRequest has its own command identity because one admission can carry many packets.
type AdmitRequest struct {
	CommandID model.ID
	PacketIDs []model.ID
	Admitter  model.Actor
	Outcome   string
	Reason    string
}

// Admit is the only canonical writer in this package. Capture never calls it implicitly.
//
// Everything is read under the admission lock, once. A retry of an admission
// already published is answered from the ledger alone: the published bundle
// binds the packet refs this request named, so its digest is recomputed from
// them and intake is never needed, which is what lets a moved or cloned project
// recover a lost acknowledgement, and why unreadable or corrupt intake cannot
// fail it either. Otherwise intake is read and verified once, and that one
// verified read supplies the packets, their exact-byte refs and the request
// digest for the rest of this admission.
func Admit(ctx context.Context, project store.Project, request AdmitRequest) (model.Bundle, error) {
	ids, err := admissionIDs(request)
	if err != nil {
		return model.Bundle{}, err
	}
	return store.TransactAdmission(ctx, project, store.Admission{
		ID: request.CommandID,
		RetryDigest: func(published model.Bundle) (model.Digest, error) {
			if len(published.Packets) != len(ids) {
				return "", admissionFault("conflict", "packets", "this admission id already published a different packet set")
			}
			for i, ref := range published.Packets {
				if ref.CommandID != ids[i] {
					return "", admissionFault("conflict", "packets", "this admission id already published a different packet set")
				}
			}
			return retryDigest(project, request, published.Packets)
		},
		Propose: func(prefix []model.Bundle) (model.Digest, model.Bundle, error) {
			proposal, digest, err := proposeAdmission(ctx, project, request, ids, prefix)
			return digest, proposal, err
		},
	})
}

// retryDigest recomputes the request digest against an admission already
// published under this id, from the ledger alone. The published bundle is
// authoritative: its packet refs bound the exact packet bytes when it was
// admitted, so they stand for the packets whatever intake now holds, and
// missing, unreadable or corrupt intake cannot fail an identical retry. A
// request differing in the packet set, actor, outcome or reason still yields a
// different digest and is refused.
func retryDigest(project store.Project, request AdmitRequest, published []model.PacketRef) (model.Digest, error) {
	return admissionDigest(project, request, published)
}

// admissionDigest is the request identity: the packets by exact bytes, and the
// review the admitter asked for.
func admissionDigest(project store.Project, request AdmitRequest, refs []model.PacketRef) (model.Digest, error) {
	data, err := model.Encode(struct {
		Project model.ProjectID   `json:"project"`
		Packets []model.PacketRef `json:"packets"`
		Actor   model.Actor       `json:"actor"`
		Outcome string            `json:"outcome"`
		Reason  string            `json:"reason"`
	}{project.ID, refs, request.Admitter, request.Outcome, request.Reason})
	if err != nil {
		return "", err
	}
	return model.HashBytes(data), nil
}

// proposeAdmission runs under the lock against the prefix read there.
func proposeAdmission(ctx context.Context, project store.Project, request AdmitRequest, ids []model.ID, prefix []model.Bundle) (model.Bundle, model.Digest, error) {
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		return model.Bundle{}, "", err
	}
	verified, err := store.ReadVerifiedIntake(project, ids)
	if err != nil {
		return model.Bundle{}, "", err
	}
	packets := make([]model.Packet, len(verified))
	lockedRefs := make([]model.PacketRef, len(verified))
	for i, v := range verified {
		packets[i], lockedRefs[i] = v.Packet, v.Ref
		if prior, ok := snapshot.Review(reduce.ReviewKey{Project: project.ID, CommandID: v.Ref.CommandID}); ok {
			return model.Bundle{}, "", admissionFault("conflict", "packets", fmt.Sprintf("packet %s already has disposition %s", v.Ref.CommandID, prior.Outcome))
		}
	}
	digest, err := admissionDigest(project, request, lockedRefs)
	if err != nil {
		return model.Bundle{}, "", err
	}
	proposal, err := admissionProposal(ctx, project, request, digest, snapshot, packets, lockedRefs)
	return proposal, digest, err
}

func admissionProposal(ctx context.Context, project store.Project, request AdmitRequest, digest model.Digest, snapshot reduce.Snapshot, packets []model.Packet, lockedRefs []model.PacketRef) (model.Bundle, error) {
	proposal := model.Bundle{Admitter: request.Admitter, Packets: lockedRefs, Events: []model.Event{}}
	var eventPackets []model.ID
	if request.Outcome == "accepted" {
		var err error
		packets, err = gatePackets(project.ID, snapshot, packets)
		if err != nil {
			return model.Bundle{}, err
		}
		for _, packet := range packets {
			proposal.Events = append(proposal.Events, packet.Events...)
			for range packet.Events {
				eventPackets = append(eventPackets, packet.CommandID)
			}
		}
	}
	selfAdmission, authors, reason := admissionDetails(request, packets)
	captured := make(map[model.ID]time.Time, len(packets))
	for _, packet := range packets {
		captured[packet.CommandID] = packet.CapturedAt.UTC()
	}
	invocations, err := reviewedInvocations(request.Outcome, packets)
	if err != nil {
		return model.Bundle{}, err
	}
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: lockedRefs, Outcome: request.Outcome, Actor: request.Admitter, Reason: reason,
		SelfAdmission: selfAdmission, Invocations: invocations, Authors: authors, CapturedAt: captured, EventPackets: eventPackets})
	if err != nil {
		return model.Bundle{}, err
	}
	proposal.Events = append(proposal.Events, review)
	after, err := gateProposal(snapshot, project.ID, request.CommandID, digest, proposal)
	if err != nil {
		return model.Bundle{}, err
	}
	if request.Outcome == "accepted" {
		if err := gateDisposals(after, packets); err != nil {
			return model.Bundle{}, err
		}
		if err := materializeAdmission(ctx, project, packets); err != nil {
			return model.Bundle{}, err
		}
		if err := gateProofs(ctx, project, after, packets); err != nil {
			return model.Bundle{}, err
		}
		if err := gateQuotes(ctx, project, packets); err != nil {
			return model.Bundle{}, err
		}
	}
	// The store alone seals the envelope. The validation copy never becomes
	// the proposal, so a tail selected outside the store cannot be published.
	return proposal, nil
}

func admissionIDs(r AdmitRequest) ([]model.ID, error) {
	if !model.ValidID(r.CommandID) {
		return nil, admissionFault("invalid-field", "command_id", "admission needs its own ULID")
	}
	if err := model.ValidateSchema(r.Admitter); err != nil {
		return nil, err
	}
	if r.Outcome != "accepted" && r.Outcome != "rejected" && r.Outcome != "correction-requested" {
		return nil, admissionFault("invalid-field", "outcome", "expected accepted, rejected or correction-requested")
	}
	if model.Blank(r.Reason) || len(r.PacketIDs) == 0 {
		return nil, admissionFault("invalid-field", "request", "admission needs a reason and a nonempty packet set")
	}
	seen := map[model.ID]bool{}
	ids := make([]model.ID, 0, len(r.PacketIDs))
	for _, id := range r.PacketIDs {
		if !model.ValidID(id) || id == r.CommandID {
			return nil, admissionFault("invalid-field", "packet_ids", "packet ids must be ULIDs distinct from the admission id")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// admissionDetails renders the readable suffix from the same facts we persist.
func admissionDetails(r AdmitRequest, packets []model.Packet) (map[model.ID]model.SelfAdmissionState, map[model.ID]model.Actor, string) {
	states := make(map[model.ID]model.SelfAdmissionState, len(packets))
	authors := make(map[model.ID]model.Actor, len(packets))
	var reason strings.Builder
	reason.WriteString(r.Reason)
	for _, p := range packets {
		self := model.SelfAdmissionUnknown
		if model.SameActor(p.Author, r.Admitter) {
			self = model.SelfAdmissionTrue
		} else if !model.Blank(p.Author.ID) && !model.Blank(r.Admitter.ID) {
			self = model.SelfAdmissionFalse
		}
		states[p.CommandID], authors[p.CommandID] = self, p.Author
		author := "unknown: " + p.Author.UnknownReason
		if !model.Blank(p.Author.ID) {
			author = p.Author.ID
		}
		fmt.Fprintf(&reason, "\nPacket %s author %s. Self-admitted: %s.", p.CommandID, strconv.Quote(author), self)
	}
	return states, authors, reason.String()
}

func admissionFault(code, path, detail string) error {
	return &model.Fault{Code: code, EventIndex: -1, Path: path, Detail: detail}
}
