// This file projects verified packet headers and selected packet payloads.
// Intake storage and admission rules belong to store and write, not here.
package query

import (
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

const selectedPacketReadBudget = 256 << 20

// PacketHeader is a verified packet's bounded inventory row. Actor identity
// can itself be unknown even when the packet bytes are present.
type PacketHeader struct {
	CommandID   model.ID                              `json:"command_id"`
	Disposition string                                `json:"disposition"`
	Author      model.Actor                           `json:"author"`
	Writer      *model.Actor                          `json:"writer,omitempty"`
	CapturedAt  model.Availability[time.Time]         `json:"captured_at"`
	EventCount  model.Availability[int]               `json:"event_count"`
	EventKinds  model.Availability[[]model.EventType] `json:"event_kinds"`
	Payload     model.Availability[bool]              `json:"payload"`
	Review      *Review                               `json:"review"`
	CapturedIn  string                                `json:"captured_in,omitempty"`
}

// PacketsAnswer lists headers, or answers one command ID with its header and
// complete local packet. A missing local payload remains null with a reason.
type PacketsAnswer struct {
	ViewHeader
	Packets  []PacketHeader `json:"packets"`
	Selected *PacketHeader  `json:"header"`
	Packet   *model.Packet  `json:"packet"`
}

func packetHeader(p Packet) PacketHeader {
	h := PacketHeader{CommandID: p.CommandID, Disposition: p.Disposition, Review: p.Review, CapturedIn: p.CapturedIn}
	if p.Review != nil {
		h.Author = p.Review.Author
		if p.Review.Corrected != nil {
			writer := p.Review.Corrected.Writer
			h.Writer = &writer
		}
	}
	if p.Packet == nil {
		reason := "packet bytes are absent from local intake"
		if p.Unavailable != nil {
			reason = p.Unavailable.Reason
		}
		if p.Review == nil {
			h.Author = model.Actor{UnknownReason: reason}
		}
		h.CapturedAt = model.Availability[time.Time]{State: model.Unknown, Reason: reason}
		h.EventCount = model.Availability[int]{State: model.Unknown, Reason: reason}
		h.EventKinds = model.Availability[[]model.EventType]{State: model.Unknown, Reason: reason}
		h.Payload = model.Availability[bool]{State: model.Unknown, Reason: reason}
		return h
	}
	h.Author = p.Packet.Author
	at := p.Packet.CapturedAt
	eventCount := len(p.Packet.Events)
	kinds := []model.EventType{}
	seen := map[model.EventType]bool{}
	for _, event := range p.Packet.Events {
		if !seen[event.Type] {
			seen[event.Type] = true
			kinds = append(kinds, event.Type)
		}
	}
	available := true
	h.CapturedAt = model.Availability[time.Time]{State: model.Known, Value: &at}
	h.EventCount = model.Availability[int]{State: model.Known, Value: &eventCount}
	h.EventKinds = model.Availability[[]model.EventType]{State: model.Known, Value: &kinds}
	h.Payload = model.Availability[bool]{State: model.Known, Value: &available}
	return h
}

func packetsView(project store.Project, s reduce.Snapshot, h ViewHeader, id model.ID) (*PacketsAnswer, error) {
	packets, err := intakePackets(project, s, true, id)
	if err != nil {
		return nil, err
	}
	a := &PacketsAnswer{ViewHeader: h, Packets: []PacketHeader{}}
	for _, p := range packets {
		header := packetHeader(p)
		if id == "" {
			a.Packets = append(a.Packets, header)
			continue
		}
		if p.CommandID == id {
			a.Selected, a.Packet = &header, p.Packet
			if p.Packet == nil {
				a.Result, a.Reason = "UNKNOWN", header.Payload.Reason
			}
			return a, nil
		}
	}
	if id != "" {
		a.Result, a.Reason = "UNKNOWN", fmt.Sprintf("packet %s is not visible in this project's local intake or reviews at this watermark", id)
	}
	return a, nil
}
