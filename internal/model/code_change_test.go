package model

// R14.2 code-change shape: two different commits of one format, at least one
// clean, distinct changed path, and only beside an inapplicable disposition.

import (
	"strings"
	"testing"
)

func TestCodeChangeShape(t *testing.T) {
	head := func(c byte) GitHead { return GitHead{ObjectFormat: "sha1", Commit: strings.Repeat(string(c), 40)} }
	proof := func(disposition string, change CodeChange) *ProofAdmit {
		return &ProofAdmit{Claim: schemaRef(1), CriterionRef: schemaCriterionRef(), Verdict: VerdictSupports,
			Evidence: []ObservationDisposition{{InvocationRef: InvocationRef{Project: schemaProject, InvocationID: schemaID(9)},
				Disposition: disposition, Reason: "measured code the fix changed", CodeChange: &change}},
			Judgment: ResponsibleJudgment{Actor: Actor{ID: "coordinator"}, Reason: "re-measured after the fix"}}
	}
	good := CodeChange{From: head('a'), To: head('b'), ChangedPaths: []string{"internal/model/code_change.go"}}
	raw := requireSchemaGood(t, proof("inapplicable", good))
	for _, disposition := range []string{"supports", "contradicts", "inconclusive"} {
		if err := ValidateSchema(proof(disposition, good)); err == nil {
			t.Errorf("a code change beside %q was accepted; it only sets a run aside", disposition)
		}
	}
	for name, spoil := range map[string]func(*CodeChange){
		"same commit":    func(c *CodeChange) { c.To = c.From },
		"mixed formats":  func(c *CodeChange) { c.To = GitHead{ObjectFormat: "sha256", Commit: strings.Repeat("b", 64)} },
		"no paths":       func(c *CodeChange) { c.ChangedPaths = []string{} },
		"unclean path":   func(c *CodeChange) { c.ChangedPaths = []string{"internal/./model"} },
		"root path":      func(c *CodeChange) { c.ChangedPaths = []string{"."} },
		"duplicate path": func(c *CodeChange) { c.ChangedPaths = []string{"a.go", "a.go"} },
		"escaping path":  func(c *CodeChange) { c.ChangedPaths = []string{"../a.go"} },
	} {
		change := good
		change.ChangedPaths = append([]string{}, good.ChangedPaths...)
		spoil(&change)
		if err := ValidateSchema(proof("inapplicable", change)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	requireSchemaRefusal(t, mutateSchema(t, raw, "evidence", []any{}, false), "invalid-field")
}
