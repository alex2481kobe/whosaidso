package reduce

// Packet authorship (R10.1 revised): who wrote the packet that carried an
// admitted event, read from the review.admit that admitted it. Accountability
// is visibility, so the author is a ledger fact next to authority and quote.
// This file records and answers authorship, and computes self-admission from
// it; it never compares the author with an authority and never gates anything
// on authorship.

import (
	"datum/internal/model"
)

// PacketAuthor is the packet an admitted event came from and the Actor that
// packet was captured with. When the ledger does not say, Packet is omitted or
// Author is an unknown Actor with its reason: never a guess from nearby facts.
// The tag keeps an unattributed event from rendering a blank packet id.
type PacketAuthor struct {
	Packet model.ID `json:"Packet,omitempty"`
	Author model.Actor
}

const (
	unrecordedAuthor   = "the review that dispositioned this packet did not record its author"
	unattributedAuthor = "the ledger does not attribute this event to a reviewed packet"
)

// reviewAuthor is one packet's recorded author, or an explicit unknown when a
// legacy review omitted the whole map (validation forbids partial maps).
func reviewAuthor(e *model.ReviewAdmit, packet model.ID) model.Actor {
	if author, ok := e.Authors[packet]; ok {
		return author
	}
	return model.Actor{UnknownReason: unrecordedAuthor}
}

// attributeEvents binds each bundle event before an accepted review to the
// packet that carried it. indexBundle has already checked the list covers
// exactly those events.
func (s *state) attributeEvents(b model.Bundle, e *model.ReviewAdmit) {
	for i, packet := range e.EventPackets {
		s.eventPackets[Origin{Sequence: b.Sequence, EventIndex: i}] = ReviewKey{Project: b.Project, CommandID: packet}
	}
}

func (s *state) eventAuthor(o Origin) PacketAuthor {
	key, ok := s.eventPackets[o]
	if !ok {
		return PacketAuthor{Author: model.Actor{UnknownReason: unattributedAuthor}}
	}
	return PacketAuthor{Packet: key.CommandID, Author: s.reviews[key].Author}
}

// EventAuthor answers who wrote the packet that carried the event at o.
func (s Snapshot) EventAuthor(o Origin) PacketAuthor {
	return s.inner().eventAuthor(o)
}

// selfAdmission compares a packet's recorded author with the actor that
// admitted it: TRUE only for the same known actor, FALSE for two distinct
// known actors, UNKNOWN otherwise. Two unknown actors never match, and a
// legacy stored comparison is never consulted.
func selfAdmission(author, admitter model.Actor) model.SelfAdmissionState {
	switch {
	case model.SameActor(author, admitter):
		return model.SelfAdmissionTrue
	case knownActor(author) && knownActor(admitter):
		return model.SelfAdmissionFalse
	}
	return model.SelfAdmissionUnknown
}

func knownActor(a model.Actor) bool { return !model.Blank(a.ID) && model.Blank(a.UnknownReason) }
