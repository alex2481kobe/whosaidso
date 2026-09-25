package reduce

// The one check that an authored act names the author of the packet that
// carried it lives here: a criterion.fix's author and a proof.admit's
// judgment actor must each be the identified author the bundle's review
// recorded for that event's packet. Admission reaches it through Apply and
// Replay applies it to every such event. Recording and answering authorship
// lives in authorship.go; provenance authors of records do not live here.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// CodeAttributionMismatch is an authored act whose named actor is not the
// identified author of the packet that carried it. Admission reports this
// same code because it reaches the check through Apply.
const CodeAttributionMismatch = "attribution-mismatch"

// checkPacketAuthor refuses unless named is the known actor the bundle
// recorded as the author of event idx's packet. An event the bundle does not
// attribute, a packet without a recorded author, and an unknown author on
// either side are all refused: two unknowns never match, and a missing
// attribution is never replaced by a default actor.
func (s *state) checkPacketAuthor(b model.Bundle, idx int, named model.Actor, path string) error {
	author, ok := s.bundle.packetAuthor(idx)
	if !ok {
		return faultAt(CodeAttributionMismatch, b.Sequence, idx, path,
			"no review in this bundle records the author of the packet that carried this event")
	}
	if !model.SameActor(named, author) {
		return faultAt(CodeAttributionMismatch, b.Sequence, idx, path, "must be the identified author of the packet that carried this event")
	}
	return nil
}

// packetAuthor is the recorded author of the packet that carried event idx,
// when this bundle's accepted review attributes the event and records it.
func (f *bundleFacts) packetAuthor(idx int) (model.Actor, bool) {
	if f == nil {
		return model.Actor{}, false
	}
	packet, ok := f.packetOf[idx]
	if !ok {
		return model.Actor{}, false
	}
	author, ok := f.authors[packet]
	return author, ok
}
