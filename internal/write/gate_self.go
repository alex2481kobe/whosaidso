package write

// A project's self_admission = 'refuse' (store.Policy): an accepting review
// needs, for every packet, a known author and a known admitter who are not
// the same actor. Rejecting a packet, or asking for its correction, stays
// open to anyone. Gate only: the policy travels in whosaidso.toml, and the
// ledger does not record when it was turned on, so replay never applies it.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func gateSelfAdmission(project store.Project, admitter model.Actor, packets []model.Packet) error {
	if !project.Policy.RefuseSelfAdmission {
		return nil
	}
	for _, p := range packets {
		known := !model.Blank(p.Author.ID) && model.Blank(p.Author.UnknownReason) && !model.Blank(admitter.ID) && model.Blank(admitter.UnknownReason)
		if !known || model.SameActor(p.Author, admitter) {
			return admissionFault("self-admission-refused", "admitter", fmt.Sprintf(
				"this project refuses self-admission (self_admission = 'refuse'): packet %s needs an admitter who is a known actor other than its known author", p.CommandID))
		}
	}
	return nil
}
