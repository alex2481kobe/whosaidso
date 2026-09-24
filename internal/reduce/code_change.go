package reduce

// The ledger half of R14.2 lives here: a failing run of the proof's own
// criterion revision may be set aside as inapplicable only with a recorded
// code change that replay can check without the filesystem: the run's
// recorded head is known, clean and is the change's From; every changed path
// lies under the claim's scope; and To is the clean head every supporting
// member ran at, so "current" means the code the support measured. That git
// agrees with the recorded paths is the admission gate's (internal/write).

import (
	"fmt"
	"path"
	"strings"

	"whosaidso/internal/model"
)

// CodeCodeChange is a recorded code change the ledger contradicts. Admission
// reports this same code because it reaches the check through Apply.
const CodeCodeChange = "code-change-mismatch"

// checkCodeChange checks one member's recorded code change against the run it
// sets aside and the claim's scope.
func (s *state) checkCodeChange(b model.Bundle, idx, i int, e *model.ProofAdmit, inv Invocation, class MemberClass) error {
	c := e.Evidence[i].CodeChange
	refuse := func(detail string) error {
		return faultAt(CodeCodeChange, b.Sequence, idx, fmt.Sprintf("evidence[%d].code_change", i), detail)
	}
	if class != MemberExact || inv.Seal == nil {
		return refuse("a code change sets aside only a sealed run of the proof's exact criterion revision")
	}
	if e.Refutes() {
		return refuse("a refuting proof cites failing runs; it does not set them aside")
	}
	if head, why := cleanHead(inv.Seal.ExecutionSourceIdentity); head == nil {
		return refuse("the run's " + why + ", so no code change since it can be established")
	} else if *head != c.From {
		return refuse("from is not the commit the run recorded")
	}
	scope := s.records[recordKey(e.Claim)].Claim.Scope.SourcePaths
	for j, changed := range c.ChangedPaths {
		if !underScope(changed, scope) {
			return refuse(fmt.Sprintf("changed path %d (%s) is outside the claim's scope source paths", j, changed))
		}
	}
	return nil
}

// checkCurrentCommit ties every recorded code change's To to the code the
// proof's support measured: one commit, the known clean head of every
// supporting member.
func (s *state) checkCurrentCommit(b model.Bundle, idx int, e *model.ProofAdmit) error {
	var to *model.GitHead
	for _, member := range e.Evidence {
		if c := member.CodeChange; c != nil {
			if to != nil && *to != c.To {
				return faultAt(CodeCodeChange, b.Sequence, idx, "evidence", "every code change in one proof names the same current commit")
			}
			to = &c.To
		}
	}
	if to == nil {
		return nil
	}
	for i, member := range e.Evidence {
		if member.Disposition != "supports" {
			continue
		}
		inv, class := s.memberClass(e.CriterionRef, invocationKey(member.InvocationRef))
		if class != MemberExact || inv.Seal == nil {
			continue // refused as a member already
		}
		if head, why := cleanHead(inv.Seal.ExecutionSourceIdentity); head == nil || *head != *to {
			if head != nil {
				why = "recorded head is another commit"
			}
			return faultAt(CodeCodeChange, b.Sequence, idx, fmt.Sprintf("evidence[%d]", i),
				"a supporting run must have run at the code change's current commit: its "+why)
		}
	}
	return nil
}

// cleanHead is a run's recorded commit when it is known and the checkout was
// known clean; otherwise nil and why. Unknown and dirty never qualify.
func cleanHead(id model.ExecutionIdentity) (*model.GitHead, string) {
	switch {
	case id.Head.State != model.Known || id.Head.Value == nil:
		return nil, "recorded head is unknown"
	case id.Dirty.State != model.Known || id.Dirty.Value == nil:
		return nil, "checkout state is unknown"
	case *id.Dirty.Value:
		return nil, "checkout was dirty"
	}
	return id.Head.Value, ""
}

// underScope reports whether a clean relative path lies under one of the
// scope's source paths: the path itself, or a directory above it.
func underScope(changed string, scope []string) bool {
	for _, source := range scope {
		dir := path.Clean(source)
		if dir == "." || changed == dir || strings.HasPrefix(changed, dir+"/") {
			return true
		}
	}
	return false
}
