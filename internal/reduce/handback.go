package reduce

// Who may write an attempt's terminal receipt or take an attempt over, and what
// a stopped handback must carry in its own bundle, live here. These are ledger
// rules: admission reaches them through Apply and Replay applies them to every
// bundle, so one implementation answers both. The receipt's revision
// translation is admission's clerical rewrite and stays in write.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// CodeMissingHold is a blocked-mid-task or out-of-scope receipt whose bundle
// does not carry the open hold that says who resumes it and when.
const CodeMissingHold = "missing-hold"

// checkReceiptAuthor refuses a terminal receipt whose packet author is not
// the attempt's holder. When the holder is UNKNOWN, a receipt from a NAMED
// author is admissible and stays attributed to that author through its packet:
// closing an attempt requires no authority or judgment, so unknown attribution
// must not make it unclosable (C782). Two unknowns never match, and an event
// no review attributes has no author, so it matches nobody.
func (s *state) checkReceiptAuthor(b model.Bundle, idx int, holder model.Actor) error {
	author, _ := s.bundle.packetAuthor(idx)
	ok := model.SameActor(holder, author)
	if model.Blank(holder.ID) {
		ok = !model.Blank(author.ID) && model.Blank(author.UnknownReason)
	}
	if !ok {
		return faultAt(CodeAttributionMismatch, b.Sequence, idx, "author",
			"receipt must be authored by the attempt holder, or by a named author when the holder is unknown")
	}
	return nil
}

// checkHandbackHold is the stopped-handback rule. A blocked-mid-task receipt
// needs an open hold on the same task in its own bundle, matched by task record
// id because a hold belongs to the task and survives amendments; an
// out-of-scope receipt needs a resume hold naming the actor who takes it up,
// and may not amend its own task in that bundle (reassignment, never a quiet
// scope change). A hold admitted earlier does not count, and neither does one
// this bundle also clears. Other outcomes carry no hold requirement.
func (s *state) checkHandbackHold(b model.Bundle, idx int, e *model.AttemptTerminal) error {
	if e.Outcome != model.AttemptBlockedMidTask && e.Outcome != model.AttemptOutOfScope {
		return nil
	}
	outOfScope := e.Outcome == model.AttemptOutOfScope
	cleared := map[model.ID]bool{}
	for _, event := range s.bundle.events {
		switch other := event.(type) {
		case *model.BlockerClear:
			if other.Task.Project == e.Task.Project && other.Task.RecordID == e.Task.RecordID {
				cleared[other.BlockerID] = true
			}
		case *model.TaskAmend:
			if outOfScope && other.Target.Project == e.Task.Project && other.Target.RecordID == e.Task.RecordID {
				return faultAt(CodeInvalidTransition, b.Sequence, idx, "task.amend",
					"out-of-scope handback cannot amend its task scope in the same bundle")
			}
		}
	}
	for _, event := range s.bundle.events {
		hold, ok := event.(*model.BlockerHold)
		if !ok || hold.Task.Project != e.Task.Project || hold.Task.RecordID != e.Task.RecordID || cleared[hold.BlockerID] {
			continue
		}
		if !outOfScope || hold.Reason == model.BlockerResume && !model.Blank(hold.Actor.ID) {
			return nil
		}
	}
	return faultAt(CodeMissingHold, b.Sequence, idx, "attempt.terminal",
		"blocked-mid-task needs a bundled open hold; out-of-scope needs an authored resume/reassignment hold naming its actor")
}
