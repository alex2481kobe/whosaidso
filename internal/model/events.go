package model

// The closed event interface, type tags, and encode/decode dispatch live here.
// Payload definitions, payload validation, and reference walking do not.
// This file stays below 200 lines to keep the closed event registry together.

import (
	"encoding/json"
	"reflect"
)

// TypedEvent is the closed semantic boundary after U01's raw Event. U05/U06
// consume these payloads; only admission can decide whether they fit current state.
type TypedEvent interface {
	eventPayload()
	EventType() EventType
}

func (*TaskCreate) eventPayload()               {}
func (*TaskCreate) EventType() EventType        { return "task.create" }
func (*TaskAmend) eventPayload()                {}
func (*TaskAmend) EventType() EventType         { return "task.amend" }
func (*TaskStart) eventPayload()                {}
func (*TaskStart) EventType() EventType         { return "task.start" }
func (*TaskTakeover) eventPayload()             {}
func (*TaskTakeover) EventType() EventType      { return "task.takeover" }
func (*AttemptTerminal) eventPayload()          {}
func (*AttemptTerminal) EventType() EventType   { return "attempt.terminal" }
func (*TaskClose) eventPayload()                {}
func (*TaskClose) EventType() EventType         { return "task.close" }
func (*BlockerHold) eventPayload()              {}
func (*BlockerHold) EventType() EventType       { return "blocker.hold" }
func (*BlockerClear) eventPayload()             {}
func (*BlockerClear) EventType() EventType      { return "blocker.clear" }
func (*InvocationStart) eventPayload()          {}
func (*InvocationStart) EventType() EventType   { return "invocation.start" }
func (*InvocationSeal) eventPayload()           {}
func (*InvocationSeal) EventType() EventType    { return "invocation.seal" }
func (*SourceIntake) eventPayload()             {}
func (*SourceIntake) EventType() EventType      { return "source.intake" }
func (*ClaimAssert) eventPayload()              {}
func (*ClaimAssert) EventType() EventType       { return "claim.assert" }
func (*ClaimRevise) eventPayload()              {}
func (*ClaimRevise) EventType() EventType       { return "claim.revise" }
func (*CriterionFix) eventPayload()             {}
func (*CriterionFix) EventType() EventType      { return "criterion.fix" }
func (*ProofAdmit) eventPayload()               {}
func (*ProofAdmit) EventType() EventType        { return "proof.admit" }
func (*DecisionOpen) eventPayload()             {}
func (*DecisionOpen) EventType() EventType      { return "decision.open" }
func (*DecisionRevise) eventPayload()           {}
func (*DecisionRevise) EventType() EventType    { return "decision.revise" }
func (*DecisionDispose) eventPayload()          {}
func (*DecisionDispose) EventType() EventType   { return "decision.dispose" }
func (*Supersede) eventPayload()                {}
func (*Supersede) EventType() EventType         { return "supersede" }
func (*Correction) eventPayload()               {}
func (*Correction) EventType() EventType        { return "correction" }
func (*InstrumentDeclare) eventPayload()        {}
func (*InstrumentDeclare) EventType() EventType { return "instrument.declare" }
func (*InstrumentRevise) eventPayload()         {}
func (*InstrumentRevise) EventType() EventType  { return "instrument.revise" }
func (*TrustWithdraw) eventPayload()            {}
func (*TrustWithdraw) EventType() EventType     { return "trust.withdraw" }
func (*ReviewAdmit) eventPayload()              {}
func (*ReviewAdmit) EventType() EventType       { return "review.admit" }
func (*ArtifactDispose) eventPayload()          {}
func (*ArtifactDispose) EventType() EventType   { return "artifact.dispose" }

// DecodeEvent decodes one raw payload once, into the closed event set. Required
// fields, exact JSON key spelling and tagged unions are checked before reduction.
func DecodeEvent(raw Event) (TypedEvent, error) {
	var event TypedEvent
	switch raw.Type {
	case "task.create":
		event = &TaskCreate{}
	case "task.amend":
		event = &TaskAmend{}
	case "task.start":
		event = &TaskStart{}
	case "task.takeover":
		event = &TaskTakeover{}
	case "attempt.terminal":
		event = &AttemptTerminal{}
	case "task.close":
		event = &TaskClose{}
	case "blocker.hold":
		event = &BlockerHold{}
	case "blocker.clear":
		event = &BlockerClear{}
	case "invocation.start":
		event = &InvocationStart{}
	case "invocation.seal":
		event = &InvocationSeal{}
	case "source.intake":
		event = &SourceIntake{}
	case "claim.assert":
		event = &ClaimAssert{}
	case "claim.revise":
		event = &ClaimRevise{}
	case "criterion.fix":
		event = &CriterionFix{}
	case "proof.admit":
		event = &ProofAdmit{}
	case "decision.open":
		event = &DecisionOpen{}
	case "decision.revise":
		event = &DecisionRevise{}
	case "decision.dispose":
		event = &DecisionDispose{}
	case "supersede":
		event = &Supersede{}
	case "correction":
		event = &Correction{}
	case "instrument.declare":
		event = &InstrumentDeclare{}
	case "instrument.revise":
		event = &InstrumentRevise{}
	case "trust.withdraw":
		event = &TrustWithdraw{}
	case "review.admit":
		event = &ReviewAdmit{}
	case "artifact.dispose":
		event = &ArtifactDispose{}
	default:
		return nil, fault("unknown-event", "event.type", "event is outside the closed set: "+string(raw.Type))
	}
	tree, err := parseOrdered(raw.Data)
	if err != nil {
		return nil, err
	}
	if err = refusePlaceholders(tree, "event.data"); err != nil { // placeholder.go
		return nil, err
	}
	if err = checkJSONShape(tree, reflect.TypeOf(event), "event.data"); err != nil {
		return nil, err
	}
	// strictUnmarshal without its second ordered parse: tree already is
	// parseOrdered(raw.Data), and its duplicate-key and trailing-content
	// refusals already ran above. UTF-8 is still checked in the same order.
	if err = refuseInvalidUTF8Bytes(raw.Data, "event.data"); err != nil {
		return nil, err
	}
	if err = strictDecodeParsed(raw.Data, tree, event, "event.data"); err != nil {
		return nil, err
	}
	if err = validateValue(reflect.ValueOf(event), "event.data"); err != nil {
		return nil, err
	}
	return event, nil
}

// EncodeEvent validates through the same boundary used for untrusted bytes, so
// an in-process producer cannot bypass a refusal by constructing a Go value.
func EncodeEvent(event TypedEvent) (Event, error) {
	if event == nil || (reflect.ValueOf(event).Kind() == reflect.Pointer && reflect.ValueOf(event).IsNil()) {
		return Event{}, invalid("event", "nil typed event")
	}
	if err := ValidateSchema(event); err != nil {
		return Event{}, err
	}
	raw, err := json.Marshal(event)
	if err == nil {
		raw, err = utcBytes(raw, reflect.TypeOf(event)) // ruling R8.4
	}
	if err != nil {
		return Event{}, invalid("event.data", err.Error())
	}
	e := Event{Type: event.EventType(), Data: raw}
	decoded, err := DecodeEvent(e)
	if err != nil {
		return Event{}, err
	}
	if reflect.TypeOf(decoded) != reflect.TypeOf(event) {
		return Event{}, fault("unknown-event", "event.type", "payload is not a member of the closed set")
	}
	return e, nil
}
