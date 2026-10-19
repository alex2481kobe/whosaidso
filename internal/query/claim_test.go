package query

// The claim status an agent reads after every sync: HELD only while its
// claim applied and nothing ended or replaced it; LOST when the merge set its
// claim aside, naming who holds the task; UNKNOWN before a sync. TAKEN-OVER
// and ENDED need attributed packets and are driven through the CLI in
// internal/acceptance/claim_status_test.go.

import (
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func claimOf(t *testing.T, p store.Project, attempt model.ID) *ClaimAnswer {
	t.Helper()
	a, err := ReadClaim(p, attempt)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func startBy(who string, attempt int) *model.TaskStart {
	return &model.TaskStart{Task: testRef(1, 1), Actor: model.Actor{ID: who}, AttemptID: testID(attempt)}
}

func TestClaimLostAtAMergeNamesTheWinner(t *testing.T) {
	p := testProject(t)
	genesis := appendEvents(t, p, 100, testTask(1))
	if a := claimOf(t, p, testID(500)); a.Status != ClaimUnknown || !strings.Contains(a.Next, "git merge") {
		t.Fatalf("a claim not in this ledger is UNKNOWN and says to sync: %+v", a)
	}
	appendEvents(t, p, 101, startBy("alice", 500))
	// The other branch claimed the same task from the same tail; its command
	// id sorts later, so the merge applies alice's claim and sets bob's aside.
	publishOtherBranch(t, p, genesis, testID(900), startBy("bob", 600))

	if a := claimOf(t, p, testID(500)); a.Status != ClaimHeld || a.Holder.ID != "alice" {
		t.Fatalf("the claim that applied is held: %+v", a)
	}
	lost := claimOf(t, p, testID(600))
	if lost.Status != ClaimLost || lost.Holder.ID != "bob" || lost.Current == nil || lost.Current.Attempt != testID(500) ||
		lost.Current.Actor.ID != "alice" || !strings.Contains(lost.Next, "stop") || !strings.Contains(lost.Reason, string(testID(900))) {
		t.Fatalf("the set-aside claim is lost, names its bundle and the attempt holding the task: %+v", lost)
	}
	todo := todoOf(t, p)
	if len(todo.SetAside) != 1 || !strings.HasPrefix(todo.SetAside[0].Next, "lost claim") ||
		!strings.Contains(todo.SetAside[0].Next, string(testID(600))) || !strings.Contains(todo.SetAside[0].Next, "bob") {
		t.Fatalf("todo words a set-aside claim as a lost claim, naming the attempt and its holder: %+v", todo.SetAside)
	}
}
