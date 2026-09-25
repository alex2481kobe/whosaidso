package reduce

// The dry-run entry point for proof admission lives here: Apply that records
// every refusal the proof family and support rules find, instead of ending the
// fold at the first one. The rules themselves stay in proof_admission.go and
// proof_family.go, which route each refusal through refuseProof; nothing here
// decides whether a proof is admissible.

import "github.com/alex2481kobe/whosaidso/internal/model"

// ApplyCollectingProofRefusals folds b onto s like Apply, except that a
// proof.admit refusal is recorded and checking continues: the remaining
// members, the family closure, rejected members and claim support are still
// checked. It is for `whosaidso proof check`, which publishes nothing. The returned
// snapshot applies every proof as if it had been admitted, so it answers "what
// would the later gate stages say", never "what is admitted". Any refusal that
// is not a proof rule still ends the fold and is returned as err.
func ApplyCollectingProofRefusals(s Snapshot, b model.Bundle) (Snapshot, []error, error) {
	st := s.inner().clone()
	var refusals []error
	st.proofRefusals = &refusals
	err := st.apply(b)
	st.proofRefusals = nil
	if err != nil {
		return Snapshot{}, refusals, err
	}
	return Snapshot{st: st}, refusals, nil
}

// refuseProof is the one switch between a fold and a dry run. A fold returns
// the refusal and stops, so replay and admission keep their first-fault order.
func (s *state) refuseProof(err error) error {
	if s.proofRefusals == nil {
		return err
	}
	*s.proofRefusals = append(*s.proofRefusals, err)
	return nil
}
