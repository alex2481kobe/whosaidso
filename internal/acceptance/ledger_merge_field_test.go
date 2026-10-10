// The one optional bundle field that records which branch tips an admission
// saw (other_heads, a list of {sequence, command_id}): when a merging admission
// writes it, how strictly a reader decodes it, and that it alone decides
// whether a failing bundle "saw everything". What belongs here: that field.
// What does not: the clash cases it feeds (ledger_merge_set_aside_test.go).
package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

type mwHeadRef struct {
	Sequence  uint64   `json:"sequence"`
	CommandID model.ID `json:"command_id"`
}

// mwJoined is three branches merged and then one admission that joins them: the
// ledger's files, the joining bundle's name, and the three branch tips.
func mwJoined(t *testing.T) (files map[string][]byte, joining string, tips []mwHeadRef) {
	t.Helper()
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	var branches []*flowWorld
	for i := 0; i < 3; i++ {
		b := mwFork(t, base, 200+100*i)
		b.mwClaim("recorded on branch " + string(rune('A'+i)))
		branches = append(branches, b)
	}
	m := mwMerge(t, branches...)
	for _, name := range mwFiles(m) {
		if strings.HasPrefix(name, "00000002-") {
			tips = append(tips, mwHeadRef{2, model.ID(name[9 : len(name)-5])})
		}
	}
	m.mwClaim("joins the three branches")
	for _, name := range mwFiles(m) {
		if strings.HasPrefix(name, "00000003-") {
			joining = name
		}
	}
	if len(tips) != 3 || joining == "" {
		t.Fatalf("fixture: want three tips and one joining bundle, got %v and %q", tips, joining)
	}
	return mwBundles(m), joining, tips
}

func TestLedgerMergeJoiningAdmissionNamesEveryOtherTipInCanonicalOrder(t *testing.T) {
	files, joining, tips := mwJoined(t)
	var bundle struct {
		Sequence    uint64      `json:"sequence"`
		Predecessor model.ID    `json:"predecessor"`
		OtherHeads  []mwHeadRef `json:"other_heads"`
	}
	if err := json.Unmarshal(files[joining], &bundle); err != nil {
		t.Fatal(err)
	}
	last := tips[len(tips)-1]
	if bundle.Predecessor != last.CommandID {
		t.Errorf("the joining bundle follows %s, want the canonical last tip %s", bundle.Predecessor, last.CommandID)
	}
	if len(bundle.OtherHeads) != 2 || bundle.OtherHeads[0] != tips[0] || bundle.OtherHeads[1] != tips[1] {
		t.Errorf("other_heads is %v, want the two tips that are not the predecessor, in canonical order: %v", bundle.OtherHeads, tips[:2])
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(files[joining], &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		if !mwLinearFields[key] && key != "other_heads" {
			t.Errorf("the joining bundle carries the field %q; other_heads is the only field a split adds", key)
		}
	}
	mwReadsOK(t, mwUnion(t, files), "the joined ledger")
}

func TestLedgerMergeOtherHeadsFieldIsDecodedStrictly(t *testing.T) {
	files, joining, tips := mwJoined(t)
	predecessor := tips[2]
	head := func(h mwHeadRef) map[string]any {
		return map[string]any{"sequence": json.Number("2"), "command_id": string(h.CommandID)}
	}
	list := func(heads ...map[string]any) []any {
		out := []any{}
		for _, h := range heads {
			out = append(out, h)
		}
		return out
	}
	with := func(h map[string]any, key string, value any) map[string]any {
		c := map[string]any{}
		for k, v := range h {
			c[k] = v
		}
		if value == nil {
			delete(c, key)
		} else {
			c[key] = value
		}
		return c
	}
	cases := []struct {
		name string
		edit func(map[string]any)
		file string // "" edits the joining bundle
	}{
		{"empty list", func(f map[string]any) { f["other_heads"] = []any{} }, ""},
		{"null", func(f map[string]any) { f["other_heads"] = nil }, ""},
		{"a string", func(f map[string]any) { f["other_heads"] = "none" }, ""},
		{"an object", func(f map[string]any) { f["other_heads"] = head(tips[0]) }, ""},
		{"a case variant of the field name", func(f map[string]any) {
			f["Other_Heads"] = f["other_heads"]
			delete(f, "other_heads")
		}, ""},
		{"an unknown key inside a head", func(f map[string]any) {
			f["other_heads"] = list(with(head(tips[0]), "note", "x"), head(tips[1]))
		}, ""},
		{"a case variant of a key inside a head", func(f map[string]any) {
			h := with(head(tips[0]), "sequence", nil)
			h["Sequence"] = json.Number("2")
			f["other_heads"] = list(h, head(tips[1]))
		}, ""},
		{"a head without a command id", func(f map[string]any) {
			f["other_heads"] = list(with(head(tips[0]), "command_id", nil), head(tips[1]))
		}, ""},
		{"a head naming a bundle that is not there", func(f map[string]any) {
			f["other_heads"] = list(head(mwHeadRef{2, recID(1)}), head(tips[1]))
		}, ""},
		{"a head naming the predecessor", func(f map[string]any) {
			f["other_heads"] = list(head(tips[0]), head(predecessor))
		}, ""},
		{"a head at the bundle's own sequence", func(f map[string]any) {
			f["other_heads"] = list(with(head(tips[0]), "sequence", json.Number("3")), head(tips[1]))
		}, ""},
		{"a head listed twice", func(f map[string]any) {
			f["other_heads"] = list(head(tips[0]), head(tips[0]))
		}, ""},
		{"heads out of canonical order", func(f map[string]any) {
			f["other_heads"] = list(head(tips[1]), head(tips[0]))
		}, ""},
		{"a genesis bundle with heads", func(f map[string]any) { f["other_heads"] = list(head(tips[0])) }, "GENESIS"},
	}
	// Control: the undamaged ledger reads, so every refusal below is the edit.
	mwReadsOK(t, mwUnion(t, files), "the undamaged joined ledger")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := mwUnion(t, files)
			name := joining
			if c.file == "GENESIS" {
				name = mwFiles(w)[0]
			}
			mwEdit(t, w, name, name, c.edit)
			mwRefuses(t, w, "", c.name)
		})
	}
}

// Whether a failing bundle is damage or a clash depends on one thing: did it
// descend from every bundle before it. The predecessor names one; other_heads
// names the rest. The same failing bundle is set aside without the field and
// fatal with it.
func TestLedgerMergeOtherHeadsDecideWhetherAFailingBundleSawEverything(t *testing.T) {
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwClaim("recorded on branch A")
	b := mwFork(t, base, 300)
	b.mwClaim("recorded on branch B")
	files := mwBundles(mwMerge(t, a, b))
	names := []string{}
	for name := range files {
		if strings.HasPrefix(name, "00000002-") {
			names = append(names, name)
		}
	}
	if len(names) != 2 {
		t.Fatalf("fixture: want two bundles at sequence 2, got %v", names)
	}
	a2, b2 := names[0], names[1]
	if a2 > b2 {
		a2, b2 = b2, a2
	}
	failing := recID(802)
	name, err := model.BundleName(3, failing)
	if err != nil {
		t.Fatal(err)
	}
	// A copy of A's admission after B's: its packet is admitted a second time.
	build := func(w *flowWorld, heads []any) {
		mwEdit(t, w, a2, name, func(f map[string]any) {
			f["sequence"], f["command_id"], f["predecessor"] = json.Number("3"), string(failing), b2[9:len(b2)-5]
			if heads != nil {
				f["other_heads"] = heads
			}
		})
	}
	t.Run("without other_heads it did not see branch A and is set aside", func(t *testing.T) {
		w := mwUnion(t, files)
		build(w, nil)
		mwReadsOK(t, w, "a failing bundle that names only the canonical last")
		mwAsserted(t, w, failing)
		mwOnlyMarked(t, w, failing)
		if got := mwAsideCodes(t, w)[failing]; got == "" || got == reduce.CodeBuiltOnSetAside {
			t.Errorf("set aside with code %q, want the fault of its own events", got)
		}
	})
	t.Run("with other_heads it saw everything and the failure is fatal", func(t *testing.T) {
		w := mwUnion(t, files)
		build(w, []any{map[string]any{"sequence": json.Number("2"), "command_id": a2[9 : len(a2)-5]}})
		mwRefuses(t, w, "", "a failing bundle that names both tips")
	})
}
