package reduce

// author.correct: who really wrote an admitted packet, recorded beside the
// author its review recorded, which is never rewritten. Three rules, applied
// on admission and replay alike:
//   - each packet named was admitted (an accepted review, earlier in the
//     ledger, that this bundle's branch saw);
//   - each packet is corrected at most once;
//   - the correction's own packet was written by the writer it names, so
//     only a writer claims a packet, and never by naming its recorded author.
// Views read the writer through PacketAuthor.Writer (authorship.go).

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func (s *state) authorCorrect(b model.Bundle, idx int, o Origin, e *model.AuthorCorrect) error {
	if err := s.checkPacketAuthor(b, idx, e.Writer, "writer"); err != nil {
		return err
	}
	for i, packet := range e.Packets {
		path := fmt.Sprintf("packets[%d]", i)
		key := ReviewKey{Project: b.Project, CommandID: packet}
		r, ok := s.reviews[key]
		if !ok || r.Outcome != "accepted" {
			return faultAt(CodeUnknownReference, b.Sequence, idx, path, "no admitted packet with this command id")
		}
		if err := s.unseen(b, idx, path, "packet "+string(packet), r.Origin); err != nil {
			return err
		}
		if r.Corrected != nil {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, path,
				fmt.Sprintf("the packet's writer was already corrected at sequence %d", r.Corrected.Origin.Sequence))
		}
		if model.SameActor(r.Author, e.Writer) {
			return faultAt(CodeInvalidField, b.Sequence, idx, "writer", "names the packet's recorded author: there is nothing to correct")
		}
		r.Corrected = &AuthorCorrection{Writer: e.Writer, Reason: e.Reason, Origin: o}
		put(s, s.reviews, key, r)
	}
	return nil
}
