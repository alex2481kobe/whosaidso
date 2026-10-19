package reduce

// Packet authorship: who wrote the packet that carried an
// admitted event, read from the review.admit that admitted it. Accountability
// is visibility, so the author is a ledger fact next to authority and quote.
// This file records and answers authorship, and computes self-admission from
// it; it never compares the author with an authority and never gates anything
// on authorship.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// PacketAuthor is the packet an admitted event came from and the Actor that
// packet was captured with. When the ledger does not attribute the event,
// Packet is omitted and Author is an unknown Actor with its reason; an author
// the review recorded as unknown stays unknown: never a guess from nearby facts.
// The omitempty keeps an unattributed event from rendering a blank packet id.
// Author is exported as "actor", so a record's author reads author.actor, not
// author.author.
// WrittenBy is set when an admitted author.correct names the packet's real
// writer (author_correct.go).
type PacketAuthor struct {
	Packet    model.ID     `json:"packet,omitempty"`
	Author    model.Actor  `json:"actor"`
	WrittenBy *model.Actor `json:"written_by,omitempty"`
}

// Writer is who wrote the packet: the corrected writer when there is one,
// else the recorded author.
func (p PacketAuthor) Writer() model.Actor {
	if p.WrittenBy != nil {
		return *p.WrittenBy
	}
	return p.Author
}

const unattributedAuthor = "the ledger does not attribute this event to a reviewed packet"

// attributeEvents binds each bundle event before an accepted review to the
// packet that carried it. indexBundle has already checked the list covers
// exactly those events.
func (s *state) attributeEvents(b model.Bundle, e *model.ReviewAdmit) {
	for i, packet := range e.EventPackets {
		put(s, s.eventPackets, s.bundle.origin(i), ReviewKey{Project: b.Project, CommandID: packet})
	}
}

func (s *state) eventAuthor(o Origin) PacketAuthor {
	key, ok := s.eventPackets[o]
	if !ok {
		return PacketAuthor{Author: model.Actor{UnknownReason: unattributedAuthor}}
	}
	r := s.reviews[key]
	a := PacketAuthor{Packet: key.CommandID, Author: r.Author}
	if r.Corrected != nil {
		writer := r.Corrected.Writer
		a.WrittenBy = &writer
	}
	return a
}

// EventAuthor answers who wrote the packet that carried the event at o.
func (s Snapshot) EventAuthor(o Origin) PacketAuthor {
	return s.inner().eventAuthor(o)
}

// selfAdmission compares a packet's recorded author with the actor that
// admitted it: TRUE only for the same known actor, FALSE for two distinct
// known actors, UNKNOWN otherwise. Two unknown actors never match.
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
