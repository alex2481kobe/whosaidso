package query

// Revision differences: what one amendment changed, computed on read
// from the two recorded revisions it joins. Every revision is kept, so nothing
// here is stored: no event, field or change list. One generic, structural diff
// serves every kind (task, claim, decision, instrument) and is reused by
// history (each amending event's row) and continue (every amendment of the
// continued record). Which rows carry an amendment is the views' choice; the
// text lines live in brief_sections.go.

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

// Amendment is one recorded revision after the first: the event that made it,
// the review that admitted that event, and what changed from the revision
// before it.
type Amendment struct {
	Revision model.RecordRef `json:"revision"` // the revision this amendment made; it replaced revision-1
	Origin   reduce.Origin   `json:"origin"`   // the amending event: its bundle sequence and index
	Packet   model.ID        `json:"packet,omitempty"`
	Review   any             `json:"review"` // AmendmentReview, or Unknown when no reviewed packet carried it
	Changes  []FieldChange   `json:"changes"`
}

// AmendmentReview is the recorded review of the packet that carried the
// amendment: who admitted it and the reason they recorded.
type AmendmentReview struct {
	Actor  model.Actor `json:"actor"`
	Reason string      `json:"reason"`
}

// FieldChange is one field-level difference. Path names the field by its JSON
// keys. An array whose elements each carry an identity (an id, a target's
// record id, or a record id) is matched by it, so a plan item reads as
// prerequisites[RECORD_ID] and a retarget as its target.revision changing.
// Other arrays are compared as collections of whole values: an element is
// added or removed whole. Blind spot: a reordering alone is not a change.
type FieldChange struct {
	Path   string `json:"path"`
	Change string `json:"change"` // added, removed or changed
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// amendments are every amendment of one record, oldest first.
func amendments(s reduce.Snapshot, id reduce.Ident) []Amendment {
	out := []Amendment{}
	current, _ := s.CurrentRevision(id)
	for rev := model.Revision(2); rev <= current; rev++ {
		if r, ok := s.Record(model.RecordRef{Project: id.Project, RecordID: id.ID, Revision: rev}); ok {
			if a, ok := amendmentOf(s, r); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// amendmentOf joins revision r to the revision it replaced. A first revision
// replaced nothing and is no amendment.
func amendmentOf(s reduce.Snapshot, r reduce.Record) (Amendment, bool) {
	prior, ok := s.Record(model.RecordRef{Project: r.Key.Project, RecordID: r.Key.ID, Revision: r.Key.Revision - 1})
	if !ok {
		return Amendment{}, false
	}
	a := Amendment{Revision: asRef(r.Key), Origin: r.Origin, Changes: diffSpecs(specOf(prior), specOf(r)),
		Review: unknown("the ledger does not attribute this amendment to a reviewed packet")}
	// The sources a revision cites are part of what it changed: a new basis
	// for an unchanged spec is still a change a reader must see.
	diffValue("provenance.source_refs", decoded(prior.Provenance.SourceRefs), decoded(r.Provenance.SourceRefs), &a.Changes)
	if packet := s.EventAuthor(r.Origin).Packet; packet != "" {
		a.Packet = packet
		if review, ok := s.Review(reduce.ReviewKey{Project: r.Key.Project, CommandID: packet}); ok {
			a.Review = AmendmentReview{Actor: review.Actor, Reason: review.Reason}
		}
	}
	return a, true
}

func specOf(r reduce.Record) any {
	switch {
	case r.Task != nil:
		return r.Task
	case r.Claim != nil:
		return r.Claim
	case r.Decision != nil:
		return r.Decision
	}
	return r.Instrument
}

// diffSpecs compares two specs through their JSON export, so the paths are
// the names a reader already sees.
func diffSpecs(before, after any) []FieldChange {
	out := []FieldChange{}
	diffValue("", decoded(before), decoded(after), &out)
	return out
}

func decoded(v any) any {
	raw, _ := json.Marshal(v) // a reduced spec always encodes
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out any
	_ = decoder.Decode(&out)
	return out
}

func diffValue(path string, a, b any, out *[]FieldChange) {
	if sameValue(a, b) {
		return
	}
	am, aMap := a.(map[string]any)
	bm, bMap := b.(map[string]any)
	// An object that appears or goes (an accepter, progress) is shown field
	// by field, so its values stay readable.
	if (aMap || a == nil) && (bMap || b == nil) && (aMap || bMap) {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			diffValue(join(path, k), am[k], bm[k], out)
		}
		return
	}
	// An absent list and an empty one both say none: no element differs.
	as, aList := a.([]any)
	bs, bList := b.([]any)
	if (aList || a == nil) && (bList || b == nil) && (aList || bList) {
		diffList(path, as, bs, out)
		return
	}
	switch {
	case a == nil:
		*out = append(*out, change(path, "added", nil, b))
	case b == nil:
		*out = append(*out, change(path, "removed", a, nil))
	default:
		*out = append(*out, change(path, "changed", a, b))
	}
}

// diffList matches identified elements by identity and recurses into them;
// any other list is compared as a collection of whole values.
func diffList(path string, a, b []any, out *[]FieldChange) {
	aKeys, aOK := identities(a)
	bKeys, bOK := identities(b)
	if !aOK || !bOK {
		aKeys, bKeys = encodings(a), encodings(b)
	}
	index := map[string]int{}
	for i, k := range bKeys {
		index[k] = i
	}
	for i, k := range aKeys {
		if _, kept := index[k]; !kept {
			*out = append(*out, change(element(path, k, aOK && bOK), "removed", a[i], nil))
		}
	}
	prior := map[string]int{}
	for i, k := range aKeys {
		prior[k] = i
	}
	for i, k := range bKeys {
		if j, kept := prior[k]; !kept {
			*out = append(*out, change(element(path, k, aOK && bOK), "added", nil, b[i]))
		} else if aOK && bOK {
			diffValue(element(path, k, true), a[j], b[i], out)
		}
	}
}

// identities are the elements' own ids, when every element has one and no
// two share it. Otherwise the list has no identity to match on.
func identities(xs []any) ([]string, bool) {
	keys, seen := make([]string, 0, len(xs)), map[string]bool{}
	for _, x := range xs {
		m, _ := x.(map[string]any)
		target, _ := m["target"].(map[string]any)
		key := ""
		for _, candidate := range []any{m["id"], target["record_id"], m["record_id"]} {
			if s, ok := candidate.(string); ok && s != "" {
				key = s
				break
			}
		}
		if key == "" || seen[key] {
			return nil, false
		}
		keys, seen[key] = append(keys, key), true
	}
	return keys, true
}

// encodings name whole values; a repeated value is numbered so each copy
// counts once.
func encodings(xs []any) []string {
	keys, n := make([]string, 0, len(xs)), map[string]int{}
	for _, x := range xs {
		raw, _ := json.Marshal(x)
		n[string(raw)]++
		keys = append(keys, fmt.Sprintf("%s#%d", raw, n[string(raw)]))
	}
	return keys
}

func element(path, key string, identified bool) string {
	if identified {
		return path + "[" + key + "]"
	}
	return path
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func sameValue(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func change(path, kind string, before, after any) FieldChange {
	return FieldChange{Path: path, Change: kind, Before: before, After: after}
}
