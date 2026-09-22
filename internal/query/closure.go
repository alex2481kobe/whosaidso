package query

// Mandatory closure for context and continue: constraints, prerequisites,
// supersessions and corrections expand fully, with cycle detection, before
// any optional limit is applied. Only one-hop topic refs (context_refs) are
// optional, and only resolved ones can be cut; an unresolved link, local or
// cross-project, always stays visible. Nothing here ranks or reads prose.

import (
	"fmt"

	"datum/internal/model"
	"datum/internal/reduce"
)

type Edge struct {
	From     model.RecordRef `json:"from"`
	Relation string          `json:"relation"`
}

// ClosureNode is one exact referenced revision. Exactly one of the views or
// Unresolved is set; Current is the record's current revision when resolved.
type ClosureNode struct {
	Ref         model.RecordRef             `json:"ref"`
	Via         []Edge                      `json:"via"`
	Unresolved  *Unknown                    `json:"unresolved,omitempty"`
	Current     model.Revision              `json:"current_revision,omitempty"`
	Task        *Task                       `json:"task,omitempty"`
	Claim       *ClaimView                  `json:"claim,omitempty"`
	Decision    *DecisionView               `json:"decision,omitempty"`
	Instrument  *InstrumentView             `json:"instrument,omitempty"`
	Corrections []reduce.AdmittedCorrection `json:"corrections"`
}

type LimitReport struct {
	Requested any `json:"requested"` // int, or "none"
	Offered   int `json:"offered"`
	Omitted   int `json:"omitted"`
}

type Closure struct {
	Root      model.RecordRef     `json:"root"`
	Mandatory []ClosureNode       `json:"mandatory"`
	Cycles    [][]model.RecordRef `json:"cycles"`
	Optional  []ClosureNode       `json:"optional"`
	Limit     LimitReport         `json:"limit"`
}

type walker struct {
	s     reduce.Snapshot
	nodes map[model.RecordRef]*ClosureNode
	order []model.RecordRef
	state map[model.RecordRef]int // 1 on the DFS stack, 2 finished
	stack []model.RecordRef
	out   *Closure
}

func (w *walker) node(from model.RecordRef, relation string, to model.RecordRef) (*ClosureNode, bool) {
	n, seen := w.nodes[to]
	if !seen {
		n = &ClosureNode{Ref: to, Via: []Edge{}, Corrections: []reduce.AdmittedCorrection{}}
		w.nodes[to] = n
		w.order = append(w.order, to)
		resolve(w.s, n)
	}
	n.Via = append(n.Via, Edge{From: from, Relation: relation})
	return n, !seen
}

// resolve fills the view for an exact revision, or says why it cannot.
func resolve(s reduce.Snapshot, n *ClosureNode) {
	if n.Ref.Project != s.Project() {
		n.Unresolved = &Unknown{"UNKNOWN", fmt.Sprintf("cross-project reference to %s is resolved on read, and that ledger is not read here", n.Ref.Project)}
		return
	}
	rec, ok := s.Record(n.Ref)
	if !ok {
		n.Unresolved = &Unknown{"UNKNOWN", "no admitted record at this exact revision"}
		return
	}
	n.Current, _ = s.CurrentRevision(reduce.Ident{Project: n.Ref.Project, ID: n.Ref.RecordID})
	n.Corrections = correctionsOf(s, n.Ref)
	switch rec.Kind {
	case model.Task:
		n.Task = describe(s, rec).Task
	case model.Claim:
		p, _ := s.ClaimAt(n.Ref)
		v := claimView(s, p)
		n.Claim = &v
	case model.Decision:
		p, _ := s.DecisionAt(n.Ref)
		v := decisionView(s, p)
		n.Decision = &v
	case model.Instrument:
		p, _ := s.InstrumentAt(n.Ref)
		v, _ := instrumentView(s, p)
		n.Instrument = &v
	}
}

// edges are the authored mandatory links, then admitted supersessions and
// corrections, in ledger order. cycles counts only for authored dependency
// and supersession chains; a correction naming two records links them both ways.
type link struct {
	relation string
	to       model.RecordRef
	cycles   bool
}

func (w *walker) edges(ref model.RecordRef) (out []link) {
	add := func(relation string, to model.RecordRef, cycles bool) { out = append(out, link{relation, to, cycles}) }
	if rec, ok := w.s.Record(ref); ok && rec.Task != nil {
		for _, c := range rec.Task.ConstraintRefs {
			add("constraint", c, true)
		}
		for _, p := range rec.Task.Prerequisites {
			add("prerequisite:"+p.Kind, p.Target, true)
		}
	}
	for _, e := range w.s.Supersessions() {
		// A supersession of revision N also stands over earlier revisions of
		// that record, but never over its own replacement or later revisions.
		prior := e.Supersede.Prior
		if same(prior, ref) && ref.Revision <= prior.Revision && e.Supersede.Replacement != ref {
			add("superseded-by", e.Supersede.Replacement, true)
		}
	}
	for _, c := range w.s.Corrections() {
		for _, named := range correctionTouches(c.Correction, ref) {
			if named != ref {
				add("correction-affects", named, false)
			}
		}
	}
	return out
}

func (w *walker) visit(ref model.RecordRef) {
	w.state[ref] = 1
	w.stack = append(w.stack, ref)
	if n := w.nodes[ref]; n == nil || n.Unresolved == nil {
		for _, e := range w.edges(ref) {
			switch w.state[e.to] {
			case 1:
				if e.cycles {
					w.out.Cycles = append(w.out.Cycles, cyclePath(w.stack, e.to))
				}
				if e.to != w.out.Root {
					w.node(ref, e.relation, e.to)
				}
			case 2:
				w.node(ref, e.relation, e.to)
			default:
				w.node(ref, e.relation, e.to)
				w.visit(e.to)
			}
		}
	}
	w.stack = w.stack[:len(w.stack)-1]
	w.state[ref] = 2
}

func cyclePath(stack []model.RecordRef, back model.RecordRef) []model.RecordRef {
	for i, r := range stack {
		if r == back {
			return append(append([]model.RecordRef{}, stack[i:]...), back)
		}
	}
	return []model.RecordRef{back}
}

// closure expands everything mandatory from root, then offers optional topic
// refs, and only then applies limit (0 = none) to resolved optional nodes.
func closure(s reduce.Snapshot, root model.RecordRef, limit int) Closure {
	c := Closure{Root: root, Mandatory: []ClosureNode{}, Cycles: [][]model.RecordRef{}, Optional: []ClosureNode{}}
	w := &walker{s: s, nodes: map[model.RecordRef]*ClosureNode{}, state: map[model.RecordRef]int{}, out: &c}
	w.visit(root)
	for _, ref := range w.order {
		c.Mandatory = append(c.Mandatory, *w.nodes[ref])
	}
	var topics []model.RecordRef
	if rec, ok := s.Record(root); ok {
		switch {
		case rec.Task != nil:
			topics = append(append(topics, rec.Task.ContextRefs...), rec.Task.Scope.ContextRefs...)
		case rec.Claim != nil:
			topics = rec.Claim.Scope.ContextRefs
		case rec.Decision != nil:
			topics = rec.Decision.Scope.ContextRefs
		}
	}
	offered := map[model.RecordRef]bool{}
	kept := 0
	for _, ref := range topics {
		if ref == root || w.nodes[ref] != nil || offered[ref] {
			continue
		}
		offered[ref] = true
		n := ClosureNode{Ref: ref, Via: []Edge{{From: root, Relation: "context"}}, Corrections: []reduce.AdmittedCorrection{}}
		resolve(s, &n)
		c.Limit.Offered++
		if n.Unresolved == nil && limit > 0 && kept >= limit {
			c.Limit.Omitted++
			continue
		}
		if n.Unresolved == nil {
			kept++
		}
		c.Optional = append(c.Optional, n)
	}
	c.Limit.Requested = "none"
	if limit > 0 {
		c.Limit.Requested = limit
	}
	return c
}
