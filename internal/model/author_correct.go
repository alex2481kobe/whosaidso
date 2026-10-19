package model

import "fmt"

// AuthorCorrect records that admitted packets were written by someone other
// than the author their review recorded: for example receipts captured under
// a departed holder's name by whoever recorded them on the owner's word. It
// rewrites nothing; the review keeps its recorded author and views show the
// writer beside it. The reducer (reduce/author_correct.go) checks that each
// packet was admitted, is corrected once, and that the correction's own
// packet was written by the writer it names: only a writer claims a packet.
type AuthorCorrect struct {
	Packets []ID   `json:"packets"`
	Writer  Actor  `json:"writer"`
	Reason  string `json:"reason" semantic:"text"`
}

func (*AuthorCorrect) eventPayload()        {}
func (*AuthorCorrect) EventType() EventType { return "author.correct" }

func (e AuthorCorrect) validate(p string) error {
	if len(e.Packets) == 0 {
		return invalid(p+".packets", "name at least one admitted packet")
	}
	seen := map[ID]bool{}
	for i, id := range e.Packets {
		if seen[id] {
			return invalid(fmt.Sprintf("%s.packets[%d]", p, i), "duplicate packet")
		}
		seen[id] = true
	}
	if Blank(e.Writer.ID) || !Blank(e.Writer.UnknownReason) {
		return invalid(p+".writer", "the writer must be a known actor")
	}
	return nil
}
