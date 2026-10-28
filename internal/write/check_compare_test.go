package write

// Each class is compared independently; increases cannot be paid for by a
// decrease elsewhere, and unknown on either side always refuses.

import (
	"context"
	"strings"
	"testing"
)

func completeLedgerCheck() *LedgerCheck {
	a := &LedgerCheck{Classes: unmeasuredLedgerClasses()}
	for _, name := range ledgerClassNames {
		a.Classes[name] = measuredLedgerClass(nil)
	}
	return a
}

func TestLedgerComparisonRequiresEveryClassMeasuredAndNonincreasing(t *testing.T) {
	if a := compareLedgerChecks(completeLedgerCheck(), completeLedgerCheck()); !a.Allowed {
		t.Fatalf("measured-zero control: %+v", a)
	}
	for _, name := range ledgerClassNames {
		t.Run(name, func(t *testing.T) {
			base, head := completeLedgerCheck(), completeLedgerCheck()
			head.Classes[name] = measuredLedgerClass([]string{"new"})
			a := compareLedgerChecks(base, head)
			want := "refused: " + name + " increased: 0 -> 1"
			if a.Allowed || !strings.Contains(strings.Join(a.Refusals, "\n"), want) {
				t.Fatalf("increase: %+v", a)
			}
			for _, side := range []string{"base", "head"} {
				base, head = completeLedgerCheck(), completeLedgerCheck()
				unknown := base
				if side == "head" {
					unknown = head
				}
				unknown.Classes[name] = LedgerClass{State: "unmeasured"}
				a = compareLedgerChecks(base, head)
				want = "refused: " + name + " unmeasured at " + side
				if a.Allowed || !strings.Contains(strings.Join(a.Refusals, "\n"), want) {
					t.Fatalf("unknown %s: %+v", side, a)
				}
			}
			base, head = completeLedgerCheck(), completeLedgerCheck()
			base.Classes[name] = measuredLedgerClass([]string{"old"})
			head.Classes[name] = measuredLedgerClass([]string{"new"})
			a = compareLedgerChecks(base, head)
			c := a.Classes[name]
			if !a.Allowed || strings.Join(c.Added, ",") != "new" || strings.Join(c.Resolved, ",") != "old" {
				t.Fatalf("equal count churn hidden: %+v", a)
			}
			head.Classes[name] = measuredLedgerClass(nil)
			if a = compareLedgerChecks(base, head); !a.Allowed || strings.Join(a.Classes[name].Resolved, ",") != "old" {
				t.Fatalf("decrease: %+v", a)
			}
		})
	}
	base, head := completeLedgerCheck(), completeLedgerCheck()
	base.Classes["git_pins"] = measuredLedgerClass([]string{"old", "older"})
	head.Classes["not_committed"] = measuredLedgerClass([]string{"new"})
	if a := compareLedgerChecks(base, head); a.Allowed {
		t.Fatalf("aggregate fell but not_committed rose: %+v", a)
	}
	delete(base.Classes, "git_pins")
	if a := compareLedgerChecks(base, completeLedgerCheck()); a.Allowed {
		t.Fatalf("missing class must be unmeasured: %+v", a)
	}
}

func TestLedgerComparisonReportsUnmeasuredWhenCheckoutCheckCannotRun(t *testing.T) {
	base := ledgerFixture(t)
	head := newAdmissionFixture(t)
	a, err := CheckLedgerAgainst(context.Background(), base.project, head.project)
	if err != nil {
		t.Fatalf("check failure needs class measurements and refusals: %v", err)
	}
	if a.Allowed || !strings.Contains(strings.Join(a.Refusals, "\n"), "refused: git_pins unmeasured at head") {
		t.Fatalf("unreadable head: %+v", a)
	}
	if a.Base.Classes["replay_error"].State != "measured" || a.Head.Classes["replay_error"].State != "unmeasured" {
		t.Fatalf("check failure is not a measured replay failure: %+v", a)
	}
	if _, err := CheckLedgerAgainst(context.Background(), head.project, base.project); err != nil {
		t.Fatal(err)
	}
	head.project.ID = "different/project"
	if _, err := CheckLedgerAgainst(context.Background(), base.project, head.project); err == nil {
		t.Fatal("different projects are not comparable")
	}
}
