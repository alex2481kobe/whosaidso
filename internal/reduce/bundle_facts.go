package reduce

// The candidate bundle's inventory lives here: every event decoded once before
// any transition runs, the accepted review's event-to-packet mapping, intake
// capture stamps, candidate invocation starts and seals, and the invocation
// facts non-accepted reviews recorded. Transitions still run one event at a
// time against the state before them; this inventory only answers questions
// about the whole bundle (who carried an event, when intake captured it, what
// else this bundle admits) and never makes a later event a resolvable
// reference. Proof family rules that read it do not live here.

import (
	"fmt"
	"time"

	"datum/internal/model"
)

// bundleFacts is the inventory of one candidate bundle. It exists only while
// that bundle is applied; a snapshot never carries it.
type bundleFacts struct {
	events   []model.TypedEvent
	packetOf map[int]model.ID         // event index -> the packet that carried it
	captured map[model.ID]time.Time   // packet -> the time intake captured it
	authors  map[model.ID]model.Actor // packet -> the author it was captured with
	starts   map[InvocationKey]int    // candidate invocation.start -> event index
	seals    map[InvocationKey]int    // candidate invocation.seal -> event index
	rejected []RejectedFact
}

// RejectedFact is one invocation start or seal that a rejected or
// correction-requested review recorded (R10.3), with the review that recorded it.
type RejectedFact struct {
	Review  ReviewKey                `json:"review"`
	Outcome string                   `json:"outcome"`
	Origin  Origin                   `json:"origin"`
	Fact    model.ReviewedInvocation `json:"fact"`
}

// indexBundle decodes every event once and indexes what the whole bundle says.
// Its refusals are ones a sequential fold also makes, found here first.
func indexBundle(b model.Bundle) (*bundleFacts, error) {
	f := &bundleFacts{
		events:   make([]model.TypedEvent, 0, len(b.Events)),
		packetOf: map[int]model.ID{},
		captured: map[model.ID]time.Time{},
		authors:  map[model.ID]model.Actor{},
		starts:   map[InvocationKey]int{},
		seals:    map[InvocationKey]int{},
	}
	for i, raw := range b.Events {
		typed, err := model.DecodeEvent(raw)
		if err != nil {
			// Keep the model's own diagnostic and add where in the ledger it is.
			if flt, ok := err.(*model.Fault); ok {
				return nil, faultAt(flt.Code, b.Sequence, i, flt.Path, flt.Detail)
			}
			return nil, faultAt(CodeInvalidField, b.Sequence, i, "bundle.events", err.Error())
		}
		f.events = append(f.events, typed)
		switch e := typed.(type) {
		case *model.InvocationStart:
			first(f.starts, InvocationKey{Project: b.Project, InvocationID: e.Envelope.InvocationID}, i)
		case *model.InvocationSeal:
			first(f.seals, invocationKey(e.StartRef), i)
		case *model.ReviewAdmit:
			if err := f.indexReview(b, i, e); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

// first keeps the earliest index; a duplicate is the transition's refusal.
func first(m map[InvocationKey]int, k InvocationKey, i int) {
	if _, ok := m[k]; !ok {
		m[k] = i
	}
}

// indexReview records one review's attribution, authors, capture stamps and rejected
// facts. An event_packets list must cover exactly the events before the
// review, because a shorter or longer one would attribute an event to the
// wrong author.
func (f *bundleFacts) indexReview(b model.Bundle, idx int, e *model.ReviewAdmit) error {
	if e.EventPackets != nil {
		if len(e.EventPackets) != idx {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "event_packets",
				fmt.Sprintf("names %d events but the review follows %d", len(e.EventPackets), idx))
		}
		for i, packet := range e.EventPackets {
			f.packetOf[i] = packet
		}
	}
	for packet, at := range e.CapturedAt {
		if _, ok := f.captured[packet]; !ok {
			f.captured[packet] = at.UTC()
		}
	}
	for packet, author := range e.Authors {
		if _, ok := f.authors[packet]; !ok {
			f.authors[packet] = author
		}
	}
	if e.Outcome == "accepted" {
		return nil
	}
	for _, fact := range e.Invocations {
		f.rejected = append(f.rejected, RejectedFact{Review: ReviewKey{Project: b.Project, CommandID: fact.Packet}, Outcome: e.Outcome,
			Origin: Origin{Sequence: b.Sequence, EventIndex: idx}, Fact: fact})
	}
	return nil
}

// laterSeal is the candidate seal of k after event idx, if this bundle has one.
func (f *bundleFacts) laterSeal(k InvocationKey, idx int) *model.InvocationEnvelope {
	if f == nil {
		return nil
	}
	if at, ok := f.seals[k]; ok && at > idx {
		return &f.events[at].(*model.InvocationSeal).Envelope
	}
	return nil
}

// laterStart is the candidate start of k after event idx, if this bundle has one.
func (f *bundleFacts) laterStart(k InvocationKey, idx int) *model.InvocationEnvelope {
	if f == nil {
		return nil
	}
	if at, ok := f.starts[k]; ok && at > idx {
		return &f.events[at].(*model.InvocationStart).Envelope
	}
	return nil
}

// laterStarts are the candidate starts after event idx, in bundle order.
func (f *bundleFacts) laterStarts(idx int) []*model.InvocationStart {
	var out []*model.InvocationStart
	if f == nil {
		return out
	}
	for _, e := range f.events[idx+1:] {
		if start, ok := e.(*model.InvocationStart); ok {
			out = append(out, start)
		}
	}
	return out
}
