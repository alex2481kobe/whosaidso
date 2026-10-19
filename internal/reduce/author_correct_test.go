package reduce

// author.correct, through Replay and Apply alike: it names admitted packets,
// once each, and only their writer may record it; views keep the recorded
// author and show the writer, and closer_authored_receipt uses the writer.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// receiptPacket is acceptLedger's receipt packet (sequence 3, event 0).
var receiptPacket = newID("PKT3A0")

func correctAs(l *ledgerBuilder, t *testing.T, author string, e *model.AuthorCorrect) {
	t.Helper()
	l.add(t, authored(l.seq+1, []model.TypedEvent{e}, author)...)
}

func ownerWrote(packets ...model.ID) *model.AuthorCorrect {
	return &model.AuthorCorrect{Packets: packets, Writer: model.Actor{ID: "owner"},
		Reason: "recorded under the departed holder's name; the owner wrote it"}
}

func TestAuthorCorrectNamesTheWriterBesideTheRecordedAuthor(t *testing.T) {
	l := acceptLedger(t, nil, "agent-a")
	correctAs(l, t, "owner", ownerWrote(receiptPacket))
	closeBy(l, t, "owner")
	s := wantBoth(t, l, "")
	p, _ := s.Task(Ident{Project: testProject, ID: newID("TSKA")})
	if p.Closure.CloserAuthoredReceipt != TruthTrue {
		t.Fatalf("the closer wrote the receipt, as corrected: want TRUE, got %s", p.Closure.CloserAuthoredReceipt)
	}
	got := s.EventAuthor(p.Attempts[0].Terminal.Origin)
	if got.Author.ID != "agent-a" || got.WrittenBy == nil || got.WrittenBy.ID != "owner" {
		t.Fatalf("the recorded author stays and the writer is shown beside it: %+v", got)
	}
}

func TestAuthorCorrectRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		author string
		event  *model.AuthorCorrect
		twice  bool
		code   string
	}{
		{"recorded by someone other than the writer", "agent-b", ownerWrote(receiptPacket), false, CodeAttributionMismatch},
		{"names no admitted packet", "owner", ownerWrote(newID("ABSENT")), false, CodeUnknownReference},
		{"corrects a packet twice", "owner", ownerWrote(receiptPacket), true, CodeInvalidTransition},
		{"names the recorded author", "agent-a", &model.AuthorCorrect{Packets: []model.ID{receiptPacket},
			Writer: model.Actor{ID: "agent-a"}, Reason: "no change"}, false, CodeInvalidField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := acceptLedger(t, nil, "agent-a")
			if tc.twice {
				correctAs(l, t, "owner", ownerWrote(receiptPacket))
				wantBoth(t, l, "")
			}
			correctAs(l, t, tc.author, tc.event)
			wantBoth(t, l, tc.code)
		})
	}
}
