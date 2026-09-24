package query

// Attention the views share: owed reasons of a blocked task, and the cycles
// and unresolved links of a closure. Selecting records and rendering them do
// not belong here.

import (
	"fmt"
	"strings"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// owedBy raises one blocked task's owed reasons: a BLOCKED task waits on
// someone acting now, the same way an OPEN decision does. Every reason owed on
// the task itself (an open hold of any kind, awaiting acceptance, owed
// reconciliation) is raised with its waiting actor, known or UNKNOWN. A
// computed unmet prerequisite is not: what it waits on is another record,
// which is listed in its own right.
func owedBy(ref model.RecordRef, name string, reasons []reduce.BlockedReason) []Attention {
	out := []Attention{}
	for _, reason := range reasons {
		if reason.Kind == reduce.ReasonPrerequisite && reason.BlockerID == "" {
			continue
		}
		why := reason.Kind + ": " + reason.Detail
		if reason.BlockerID != "" {
			why = fmt.Sprintf("%s hold %s: %s", reason.Kind, reason.BlockerID, reason.Detail)
		}
		out = append(out, Attention{Kind: "task-blocked-owed", Ref: ref, Label: name, Reason: why, WaitingActor: actor(reason.Actor)})
	}
	return out
}

// closureNotes raises every cycle and every unresolved link, mandatory or optional.
func closureNotes(c Closure) []Attention {
	out := []Attention{}
	for _, cycle := range c.Cycles {
		parts := []string{}
		for _, r := range cycle {
			parts = append(parts, fmt.Sprintf("%s@%d", r.RecordID, r.Revision))
		}
		out = append(out, Attention{Kind: "closure-cycle", Ref: c.Root, Label: "mandatory closure", Reason: strings.Join(parts, " -> ")})
	}
	for _, n := range append(append([]ClosureNode{}, c.Mandatory...), c.Optional...) {
		if n.Unresolved != nil {
			out = append(out, Attention{Kind: "unresolved-link", Ref: n.Ref, Label: "unresolved " + n.Via[0].Relation, Reason: n.Unresolved.Reason})
		}
	}
	return out
}
