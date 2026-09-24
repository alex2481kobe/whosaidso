// Package write joins immutable capture to canonical state through one admission gate.
package write

// Admission request identity, packet verification, and the transaction live here.
// Artifact resolution and durable blob preservation do not.
// This file stays near 200 lines to keep the admission transaction whole.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
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
		Propose: func(prefix store.State) (model.Digest, model.Bundle, error) {
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

// proposeAdmission runs under the lock against the prefix selected there.
func proposeAdmission(ctx context.Context, project store.Project, request AdmitRequest, ids []model.ID, prefix store.State) (model.Bundle, model.Digest, error) {
	snapshot, err := prefix.Folded()
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
	proposal, err := admissionProposal(ctx, project, request, digest, snapshot, packets, lockedRefs, nil)
	return proposal, digest, err
}

// admissionProposal is the whole gate. dry is nil for a real admission; a dry
// run (admit_check.go) passes its collector, which records each refusal and
// lets the later, independent stages run, and which stages in memory the blobs
// admission would preserve, never writing them.
func admissionProposal(ctx context.Context, project store.Project, request AdmitRequest, digest model.Digest, snapshot reduce.Snapshot, packets []model.Packet, lockedRefs []model.PacketRef, dry *dryRun) (model.Bundle, error) {
	proposal := model.Bundle{Admitter: request.Admitter, Packets: lockedRefs, Events: []model.Event{}}
	eventPackets := []model.ID{}
	if request.Outcome == "accepted" {
		var err error
		packets, err = gatePackets(project.ID, snapshot, packets)
		if err != nil {
			return model.Bundle{}, dry.stop("gate", err)
		}
		for _, packet := range packets {
			proposal.Events = append(proposal.Events, packet.Events...)
			for range packet.Events {
				eventPackets = append(eventPackets, packet.CommandID)
			}
		}
	}
	authors := make(map[model.ID]model.Actor, len(packets))
	for _, packet := range packets {
		authors[packet.CommandID] = packet.Author
	}
	captured := make(map[model.ID]model.Availability[time.Time], len(packets))
	for _, packet := range packets {
		at := packet.CapturedAt.UTC()
		captured[packet.CommandID] = model.Availability[time.Time]{State: model.Known, Value: &at}
	}
	invocations, err := reviewedInvocations(request.Outcome, packets)
	if err != nil {
		return model.Bundle{}, dry.stop("gate", err)
	}
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: lockedRefs, Outcome: request.Outcome, Actor: request.Admitter, Reason: request.Reason,
		Invocations: invocations, Authors: authors, CapturedAt: captured, EventPackets: eventPackets})
	if err != nil {
		return model.Bundle{}, dry.stop("gate", err)
	}
	proposal.Events = append(proposal.Events, review)
	after, err := gateProposal(snapshot, project.ID, request.CommandID, digest, proposal, dry)
	if err != nil {
		return model.Bundle{}, dry.stop("replay", err)
	}
	dry.replayed(after)
	if request.Outcome == "accepted" {
		if err := dry.note("disposals", gateDisposals(after, packets)); err != nil {
			return model.Bundle{}, err
		}
		if err := dry.note("artifacts", materializeAdmission(ctx, project, packets, dry)); err != nil {
			return model.Bundle{}, err
		}
		if err := dry.note("proofs", gateProofs(ctx, project, after, packets, dry)); err != nil {
			return model.Bundle{}, err
		}
		if err := dry.note("quotes", gateQuotes(ctx, project, packets, dry)); err != nil {
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
	for _, id := range r.PacketIDs {
		if !model.ValidID(id) || id == r.CommandID {
			return nil, admissionFault("invalid-field", "packet_ids", "packet ids must be ULIDs distinct from the admission id")
		}
	}
	return packetSet(r.PacketIDs), nil
}

// packetSet is the packet set an admission names: each packet once, in id
// order. Admission and its dry run (CheckAdmission) both read packets
// through it, so naming a packet twice means the same thing to both.
func packetSet(named []model.ID) []model.ID {
	seen := map[model.ID]bool{}
	ids := make([]model.ID, 0, len(named))
	for _, id := range named {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func admissionFault(code, path, detail string) error {
	return &model.Fault{Code: code, EventIndex: -1, Path: path, Detail: detail}
}
