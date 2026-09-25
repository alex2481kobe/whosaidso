package write

// Tests for the ruling-quote rule: a
// decision.dispose quote must be exactly the text its authority's selector
// reads. The admitted control and the blank-quote/unnamed-authority refusals
// live in gate_dispose_test.go.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func TestDecisionDisposeQuoteIsTheSelectedWords(t *testing.T) {
	for _, route := range []string{"contrary-source", "trimmed-quote", "case-folded-quote", "number-selected", "set-selected", "whole-selector"} {
		t.Run(route, func(t *testing.T) {
			w := newDisposeWorld(t)
			e := w.dispose(1, "approved")
			source := func(body string) {
				proofPut(t, w.f.project.Root, "rulings/other.json", body)
				e.Authority.SourceRef = proofPin(body, "rulings/other.json")
			}
			switch route {
			case "contrary-source":
				// The quote appears inside the selected words, but they say the opposite.
				source(`{"ruling":"do not ship revision one"}`)
				e.Quote = "ship revision one"
			case "trimmed-quote":
				e.Quote = "ship revision one"
			case "case-folded-quote":
				e.Quote = "  Ship revision one\n"
			case "number-selected":
				source(`{"ruling":1}`)
				e.Quote = "1"
			case "set-selected":
				source(`{"ruling":["ship revision one"]}`)
				e.Quote = "ship revision one"
			case "whole-selector":
				// A whole selector reads the source's digest; even a quote that
				// spells that digest is not the authority's words.
				e.Authority.Selector = model.Selector{Kind: "whole"}
				e.Quote = string(model.HashBytes([]byte(disposeRuling)))
			}
			w.f.refuse(w.f.request(w.f.capture(nil, e)), "quote-not-verbatim")
			if d := w.decision(t, 1); d.Status != reduce.StatusOpen {
				t.Fatalf("a quote that is not the authority's words decided the question: %+v", d)
			}
		})
	}
}
