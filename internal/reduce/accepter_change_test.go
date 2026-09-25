package reduce

// Tests for checkAccepterChange (acceptance.go) through Replay and Apply
// alike: a task's named accepter is removed or changed only by a packet that
// accepter wrote; every other amendment stays open to any author.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestAmendmentKeepsTheNamedAccepter(t *testing.T) {
	owner, agentA := model.Actor{ID: "owner"}, model.Actor{ID: "agent-a"}
	for _, tc := range []struct {
		name          string
		before, after *model.Actor
		intent        string
		author        string // "" leaves the amendment's packet author unknown
		code          string
	}{
		{"control: another author keeps the accepter", &owner, &owner, "a clearer intent", "agent-a", ""},
		{"control: the accepter removes itself", &owner, nil, "", "owner", ""},
		{"control: the accepter hands over", &owner, &agentA, "", "owner", ""},
		{"control: any author names one where none was", nil, &agentA, "", "agent-a", ""},
		{"another author removes the accepter", &owner, nil, "", "agent-a", CodeAccepterMismatch},
		{"another author replaces the accepter", &owner, &agentA, "", "agent-a", CodeAccepterMismatch},
		{"an unknown author removes the accepter", &owner, nil, "", "", CodeAccepterMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := taskSpec()
			spec.Accepter = tc.before
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID("TSKA"), Spec: spec})
			next := taskSpec()
			next.Accepter = tc.after
			if tc.intent != "" {
				next.Intent = tc.intent
			}
			amend := &model.TaskAmend{Provenance: provenance("agent-a"), Target: ref(newID("TSKA"), 1), Replacement: next}
			l.add(t, authored(l.seq+1, []model.TypedEvent{amend}, tc.author)...)
			wantBoth(t, l, tc.code)
		})
	}
	// No review attributes the amendment: it has no author to match.
	spec := taskSpec()
	spec.Accepter = &owner
	l := newLedger()
	l.bare = true
	l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID("TSKA"), Spec: spec})
	l.add(t, &model.TaskAmend{Provenance: provenance("agent-a"), Target: ref(newID("TSKA"), 1), Replacement: taskSpec()})
	wantBoth(t, l, CodeAccepterMismatch)
}
