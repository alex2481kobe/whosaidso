package main

// This file holds what a write prints: the one-line acknowledgement each
// durable act gets by default, and the second step of `capture --admit` and
// `run --admit`, which admits packets just captured and, when admission is
// refused, says honestly that the capture stands and the packets are pending.
// Flag parsing lives with each verb; no admission rule lives here.

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

// bundleName is the bundle's ledger file stem: sequence and admission id.
func bundleName(b model.Bundle) string { return fmt.Sprintf("%08d-%s", b.Sequence, b.CommandID) }

func joinIDs(ids []model.ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, " ")
}

// selfAdmissions compares each reviewed packet's recorded author with the
// admitter, as the reducer does: TRUE only for the same known actor,
// FALSE for two distinct known actors, UNKNOWN otherwise.
func selfAdmissions(b model.Bundle) map[model.ID]model.SelfAdmissionState {
	out := map[model.ID]model.SelfAdmissionState{}
	known := func(a model.Actor) bool { return !model.Blank(a.ID) && model.Blank(a.UnknownReason) }
	for _, raw := range b.Events {
		event, err := model.DecodeEvent(raw)
		review, ok := event.(*model.ReviewAdmit)
		if err != nil || !ok {
			continue
		}
		for _, p := range review.Packets {
			author := review.Authors[p.CommandID]
			switch {
			case model.SameActor(author, review.Actor):
				out[p.CommandID] = model.SelfAdmissionTrue
			case known(author) && known(review.Actor):
				out[p.CommandID] = model.SelfAdmissionFalse
			default:
				out[p.CommandID] = model.SelfAdmissionUnknown
			}
		}
	}
	return out
}

// admitAck is `admitted|rejected|correction-requested PACKET... bundle SEQ-CMD`,
// naming the packets whose author admitted them.
func admitAck(b model.Bundle, outcome string) string {
	verb := outcome
	if outcome == "accepted" {
		verb = "admitted"
	}
	ids, self := []model.ID{}, []model.ID{}
	states := selfAdmissions(b)
	for _, p := range b.Packets {
		ids = append(ids, p.CommandID)
		if states[p.CommandID] == model.SelfAdmissionTrue {
			self = append(self, p.CommandID)
		}
	}
	line := fmt.Sprintf("%s %s bundle %s", verb, joinIDs(ids), bundleName(b))
	switch {
	case len(self) > 0 && len(self) == len(ids):
		line += " self-admitted"
	case len(self) > 0:
		line += " self-admitted " + joinIDs(self)
	}
	return line + "\n"
}

// shellQuote makes a printed retry command copyable into a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// admitCommand is the command that admits packets as accepted, with the
// admission id to retry under when one is given.
func admitCommand(commandID model.ID, actor model.Actor, reason string, packets []model.ID) string {
	parts := []string{"whosaidso admit"}
	if commandID != "" {
		parts = append(parts, "--command-id", string(commandID))
	}
	if !model.Blank(actor.ID) {
		parts = append(parts, "--actor", shellQuote(actor.ID))
	}
	if reason == "" {
		reason = "REASON"
		parts = append(parts, "--outcome accepted --reason", reason)
	} else {
		parts = append(parts, "--outcome accepted --reason", shellQuote(reason))
	}
	return strings.Join(append(parts, joinIDs(packets)), " ")
}

// admission is the second step of a write --admit, in the spec's
// partial-success shape. It is always reported, including when refused.
type admission struct {
	Admission struct {
		Status    string   `json:"status"` // admitted or refused
		CommandID model.ID `json:"command_id"`
		Bundle    *string  `json:"bundle"`
		Outcome   string   `json:"outcome"`
		Reason    string   `json:"reason"`
		Refusal   *string  `json:"refusal"`
	} `json:"admission"`
	SelfAdmitted any    `json:"self_admitted"` // true, false, "UNKNOWN", or null when nothing was admitted
	Pending      bool   `json:"pending"`
	Retry        string `json:"retry,omitempty"`
}

// admitCaptured admits packets just captured, as accepted, under the same
// actor, through the normal gate and lock. It is a second act, not one
// transaction with the capture: a refusal leaves the capture standing, and
// the packets pending, and the result says so with the command to retry.
func admitCaptured(ctx context.Context, project store.Project, commandID model.ID, actor model.Actor, reason string, packets []model.ID) (admission, model.Bundle, error) {
	var a admission
	if commandID == "" {
		minted, err := model.NewID(time.Now(), rand.Reader)
		if err != nil {
			return a, model.Bundle{}, err
		}
		commandID = minted
	}
	a.Admission.CommandID, a.Admission.Outcome, a.Admission.Reason = commandID, "accepted", reason
	bundle, err := write.Admit(ctx, project, write.AdmitRequest{CommandID: commandID, PacketIDs: packets, Admitter: actor, Outcome: "accepted", Reason: reason})
	if err != nil {
		refusal := err.Error()
		a.Admission.Status, a.Admission.Refusal, a.Pending = "refused", &refusal, true
		a.Retry = admitCommand(commandID, actor, reason, packets)
		return a, bundle, err
	}
	name := bundleName(bundle)
	a.Admission.Status, a.Admission.Bundle = "admitted", &name
	states := selfAdmissions(bundle)
	var self model.SelfAdmissionState
	for i, id := range packets {
		if i > 0 && states[id] != self {
			self = model.SelfAdmissionUnknown
			break
		}
		self = states[id]
	}
	switch self {
	case model.SelfAdmissionTrue:
		a.SelfAdmitted = true
	case model.SelfAdmissionFalse:
		a.SelfAdmitted = false
	default:
		a.SelfAdmitted = "UNKNOWN"
	}
	return a, bundle, nil
}

// reportAdmission prints the second step's text and turns a refusal into the
// partial-success exit: the first act succeeded, the second did not.
func reportAdmission(stdout io.Writer, jsonOutput bool, a admission, bundle model.Bundle, err error, packets []model.ID) error {
	if err == nil {
		if !jsonOutput {
			_, werr := io.WriteString(stdout, admitAck(bundle, "accepted"))
			return werr
		}
		return nil
	}
	if !jsonOutput {
		stays := "packet stays"
		if len(packets) > 1 {
			stays = "packets stay"
		}
		fmt.Fprintf(stdout, "captured %s; admission %s refused: %s; %s pending\nretry: %s\n",
			joinIDs(packets), a.Admission.CommandID, *a.Admission.Refusal, stays, a.Retry)
	}
	return &exitError{code: 4, msg: fmt.Sprintf("partial success: captured %s, admission refused: %v", joinIDs(packets), err)}
}
