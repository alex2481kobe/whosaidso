package reduce

// Blocked reasons for unmet prerequisites are listed in prerequisite index
// order, compared as numbers, never as the text of their detail.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestPrerequisiteReasonsFollowNumericIndexOrder(t *testing.T) {
	l := newLedger()
	opts := []taskOpt{}
	for i := 0; i < 12; i++ {
		id := newID(fmt.Sprintf("DEP%d", i))
		l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: id, Spec: taskSpec()})
		opts = append(opts, withPrerequisite("task-success", ref(id, 1), "forbid", nil))
	}
	l.add(t, &model.TaskCreate{Provenance: provenance("agent-b"), ID: newID("PRNT"), Spec: taskSpec(opts...)})
	p := projectTask(t, l, newID("PRNT"))
	if len(p.Reasons) != 12 {
		t.Fatalf("control: twelve unmet prerequisites must give twelve reasons, got %d", len(p.Reasons))
	}
	for i, r := range p.Reasons {
		if want := fmt.Sprintf("prerequisite %d ", i); !strings.HasPrefix(r.Detail, want) {
			t.Fatalf("reason %d is %q; reasons follow prerequisite index order (2 before 10), not text order", i, r.Detail)
		}
	}
}
