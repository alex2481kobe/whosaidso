package query

// show ID reads the record's current revision directly. Its one record must be
// exactly the entry unscoped show lists for that id, never another revision or
// another record.

import (
	"reflect"
	"testing"

	"whosaidso/internal/model"
)

func TestShowOneIsUnscopedShowsEntryForThatID(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	spec := testTask(1).Spec
	spec.Intent = "the amended intent is the current revision"
	appendEvents(t, p, 101, testTask(2), testTask(3),
		&model.TaskAmend{Target: testRef(1, 1), ExpectedRevision: 1, Replacement: spec, Provenance: testTask(1).Provenance})
	all := view_(t, p, ViewRequest{View: "show"}).(*ShowAnswer)
	if len(all.Records) != 3 {
		t.Fatalf("control: unscoped show lists %d current records, want 3", len(all.Records))
	}
	for _, want := range all.Records {
		one := showOf(t, p, want.Fact.Key.ID)
		if len(one.Records) != 1 || !reflect.DeepEqual(one.Records[0], want) {
			t.Fatalf("show %s = %+v, want unscoped show's entry %+v", want.Fact.Key.ID, one.Records, want)
		}
	}
	if one := showOf(t, p, testID(1)); one.Records[0].Fact.Key.Revision != 2 || one.Records[0].Task.Revision != 2 {
		t.Fatalf("show of an amended task must answer its current revision, got %+v", one.Records[0].Fact.Key)
	}
	if absent := showOf(t, p, testID(9)); absent.Result != "UNKNOWN" || len(absent.Records) != 0 {
		t.Fatalf("an absent id must stay UNKNOWN with no record, got %s %+v", absent.Result, absent.Records)
	}
}
