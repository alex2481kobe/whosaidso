// What a merge must not excuse: structural damage and a semantic failure that
// saw every earlier bundle stay fatal, and a ledger that never split answers
// exactly as it always did. What belongs here: refusal cases, each opening with
// an undamaged control, and the linear-ledger regression guard. What does not:
// clashes that are set aside (ledger_merge_set_aside_test.go).
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// mwSplit is base, then A and B each with claims, merged: files sort base,
// A's sequence 2, B's sequence 2, A's sequence 3.
func mwSplit(t *testing.T) *flowWorld {
	t.Helper()
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwClaim("recorded on branch A")
	a.mwClaim("recorded on branch A after its first")
	b := mwFork(t, base, 300)
	b.mwClaim("recorded on branch B")
	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "the control: the undamaged split ledger")
	return m
}

// mwLinear is a ledger that never split.
func mwLinear(t *testing.T) *flowWorld {
	t.Helper()
	w := mwWorld(t, 100)
	for _, text := range []string{"one", "two", "three"} {
		w.mwClaim(text)
	}
	mwReadsOK(t, w, "the control: the undamaged linear ledger")
	return w
}

// mwEdit rewrites one published bundle file's top-level fields in place, or
// under a new name when rename is given.
func mwEdit(t *testing.T, w *flowWorld, name, rename string, edit func(map[string]any)) {
	t.Helper()
	var fields map[string]any
	dec := json.NewDecoder(bytes.NewReader(w.ledger()[name]))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if rename != "" {
		name = rename
	}
	w.put(".whosaidso/events/"+name, body)
}

// mwRefuses fails unless every read and every admission refuses. A code names
// the fault the refusal must carry; "" means only that it is not the fork
// refusal a merge used to cause, and "*" accepts any fault.
func mwRefuses(t *testing.T, w *flowWorld, code, what string) {
	t.Helper()
	for _, args := range mwReads {
		out, err := w.cli(nil, args...)
		switch {
		case err == nil:
			t.Errorf("%s: whosaidso %s succeeded; damage must stop every read, not be set aside", what, strings.Join(args, " "))
		case code == "":
			if strings.Contains(err.Error(), "ledger-fork") {
				t.Errorf("%s: whosaidso %s refused as a fork, which a merge is not: %v", what, strings.Join(args, " "), err)
			}
		case code != "*" && !strings.Contains(err.Error(), code):
			t.Errorf("%s: whosaidso %s refused without %s: %v", what, strings.Join(args, " "), code, err)
		}
		if err != nil && bytes.Contains(out, []byte(`"view"`)) {
			t.Errorf("%s: whosaidso %s refused after printing an answer:\n%s", what, strings.Join(args, " "), out)
		}
	}
	packet := w.capture(flowAgent, w.mwClaimEvent(w.id(), "after damage"))
	before := mwBundles(w)
	if err := w.mwAdmitPacket(w.id(), packet); err == nil {
		t.Errorf("%s: admission appended to a damaged ledger", what)
	}
	if !reflect.DeepEqual(before, mwBundles(w)) {
		t.Errorf("%s: a refused admission changed the ledger", what)
	}
}

func TestLedgerMergeStructuralDamageStaysFatal(t *testing.T) {
	cases := []struct {
		name, code string
		damage     func(t *testing.T, w *flowWorld, files []string)
	}{
		{"sequence hole", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, "00000009"+last[8:], func(f map[string]any) { f["sequence"] = json.Number("9") })
			mwRemove(t, w, last)
		}},
		{"predecessor names a bundle that is not there", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, last, func(f map[string]any) { f["predecessor"] = string(recID(777)) })
		}},
		{"predecessor names a bundle two sequences down", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, last, func(f map[string]any) { f["predecessor"] = files[0][9 : len(files[0])-5] })
		}},
		{"body sequence disagrees with its filename", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, mid, func(f map[string]any) { f["sequence"] = json.Number("7") })
		}},
		{"bundle of another project", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, mid, func(f map[string]any) { f["project"] = "someone/else" })
		}},
		{"unreadable bundle", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			w.put(".whosaidso/events/"+files[len(files)-2], []byte("{ not a bundle"))
		}},
		{"second genesis", "*", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, "00000001"+mid[8:], func(f map[string]any) { delete(f, "predecessor"); f["sequence"] = json.Number("1") })
			mwRemove(t, w, mid)
		}},
	}
	for _, ledger := range []struct {
		name  string
		build func(*testing.T) *flowWorld
	}{{"linear ledger", mwLinear}, {"split ledger", mwSplit}} {
		for _, c := range cases {
			t.Run(ledger.name+"/"+c.name, func(t *testing.T) {
				w := ledger.build(t)
				c.damage(t, w, mwFiles(w))
				mwRefuses(t, w, c.code, c.name)
			})
		}
	}
}

// mwDuplicate publishes a copy of bundle `from` as the next sequence after it
// under a new command id: a bundle that descends from it and whose packet is
// admitted a second time, which no replay accepts.
func mwDuplicate(t *testing.T, w *flowWorld, from string, id model.ID) {
	t.Helper()
	var prev struct {
		Sequence  uint64
		CommandID model.ID `json:"command_id"`
	}
	if err := json.Unmarshal(w.ledger()[from], &prev); err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(prev.Sequence+1, id)
	if err != nil {
		t.Fatal(err)
	}
	mwEdit(t, w, from, name, func(f map[string]any) {
		f["sequence"] = json.Number(strconv.FormatUint(prev.Sequence+1, 10))
		f["command_id"], f["predecessor"] = string(id), string(prev.CommandID)
	})
}

func TestLedgerMergeSemanticFailureThatSawEverythingIsDamageNotAClash(t *testing.T) {
	t.Run("ledger that never split", func(t *testing.T) {
		w := mwWorld(t, 100)
		w.mwClaim("one")
		w.mwClaim("two")
		mwReadsOK(t, w, "control: a never-split ledger")
		files := mwFiles(w)
		mwDuplicate(t, w, files[1], recID(801))
		mwRefuses(t, w, "", "a hand-written bundle that readmits a packet on a ledger that never split")
	})
	t.Run("split ledger, bundle that descends from every bundle before it", func(t *testing.T) {
		w := mwSplit(t)
		w.mwClaim("after the merge") // names every head
		mwReadsOK(t, w, "control: the merged ledger with its merging admission")
		files := mwFiles(w)
		mwDuplicate(t, w, files[len(files)-1], recID(802))
		mwRefuses(t, w, "", "a failing bundle that saw both branches")
	})
}

// ---- a ledger that never split reads as before ---------------------------------

func TestLedgerMergeLinearLedgerCarriesNoNewFieldAndNoNewKeys(t *testing.T) {
	w := mwWorld(t, 100)
	w.mwClaim("one")
	w.mwClaim("two")
	keys := func(raw []byte) []string {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range top {
			got = append(got, k)
		}
		sort.Strings(got)
		return got
	}
	check := func(when string) {
		for name, body := range mwBundles(w) {
			for _, key := range keys(body) {
				if !mwLinearFields[key] {
					t.Errorf("%s: %s carries the field %q; a ledger that never split must stay readable by earlier versions, which refuse unknown fields", when, name, key)
				}
			}
		}
		out := mwReadsOK(t, w, when)
		for _, view := range []string{"todo", "history", "show"} {
			text := strings.ToLower(string(out[view]))
			if strings.Contains(text, "set aside") || strings.Contains(text, "set-aside") || mwRerecord.MatchString(text) {
				t.Errorf("%s: `whosaidso %s` mentions set-aside records on a ledger that never split:\n%s", when, view, text)
			}
		}
		want := map[string][]string{
			"todo --json":    {"attention", "awaiting_acceptance", "blocked", "in_flight", "omitted", "open_decisions", "packets_not_accepted", "project", "ready", "result", "totals", "view", "watermark"},
			"history --json": {"events", "project", "result", "reviews", "view", "watermark"},
		}
		for cmd, wantKeys := range want {
			if got := keys(out[cmd]); !reflect.DeepEqual(got, wantKeys) {
				t.Errorf("%s: `whosaidso %s` has top-level keys %v, want exactly %v; a linear ledger answers as it always did", when, cmd, got, wantKeys)
			}
		}
		var todo map[string]json.RawMessage
		_ = json.Unmarshal(out["todo --json"], &todo)
		if got := keys(todo["totals"]); len(got) != 9 {
			t.Errorf("%s: todo totals has counters %v, want the 9 it has always had", when, got)
		}
		if got := keys(todo["watermark"]); !reflect.DeepEqual(got, []string{"bundles", "events", "head", "sequence"}) {
			t.Errorf("%s: todo watermark has keys %v, want bundles, events, head, sequence", when, got)
		}
	}
	check("before another admission")
	w.mwClaim("three")
	check("after one more admission")
}
