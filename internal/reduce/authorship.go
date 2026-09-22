package reduce

// Packet authorship (R10.1 revised): who wrote the packet that carried an
// admitted event, read from the review.admit that admitted it. Accountability
// is visibility, so the author is a ledger fact next to authority and quote.
// This file records and answers authorship only; it never compares the author
// with an authority or an admitter, and never gates anything on it.

import (
	"fmt"

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
// packet that carried it. The list must cover exactly those events, because a
// shorter or longer one would attribute an event to the wrong author.
func (s *state) attributeEvents(b model.Bundle, idx int, e *model.ReviewAdmit) error {
	if e.EventPackets == nil {
		return nil
	}
	if len(e.EventPackets) != idx {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "event_packets",
			fmt.Sprintf("names %d events but the review follows %d", len(e.EventPackets), idx))
	}
	for i, packet := range e.EventPackets {
		s.eventPackets[Origin{Sequence: b.Sequence, EventIndex: i}] = ReviewKey{Project: b.Project, CommandID: packet}
	}
	return nil
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
