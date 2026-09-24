package write

// The ruling-quote rule (R10.1 follow-through, U14 review): an owner act that
// carries a quote and an authority is admitted only when the quote is exactly
// the text the authority's selector reads from its pinned source. Today only
// decision.dispose carries a quote; supersede and artifact.dispose carry an
// authority and no quote, so there is nothing of theirs to compare. Whether the
// authority is named and its carrier resolves is checked elsewhere
// (gate_proof.go, gate_supersede.go, admit_artifacts.go); this file only
// compares words.

import (
	"context"
	"fmt"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// gateQuotes runs after admission artifacts resolved, so the authority's
// source is available from its locators or the preserved blob store.
func gateQuotes(ctx context.Context, project store.Project, packets []model.Packet, dry *dryRun) error {
	resolver := dry.resolver(project)
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			if e, ok := event.(*model.DecisionDispose); ok {
				if err := gateQuote(ctx, resolver, e.Quote, e.Authority); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// gateQuote compares bytes exactly: no trimming, case folding or Unicode
// normalization. A selection that is not a single JSON string (a number, an
// object without a value, a set, an absent pointer, or a whole-artifact
// selector, whose reading is a digest) cannot be compared with words, so the
// quote is unverified and the act is refused rather than assumed to match.
func gateQuote(ctx context.Context, resolver *evidence.Resolver, quote string, authority model.Authority) error {
	if authority.Selector.Kind != "json-pointer" {
		return admissionFault("quote-not-verbatim", "authority.selector",
			"the authority's selector reads the source's identity, not text; select the ruling's words so the quote can be compared")
	}
	ref := authority.SourceRef
	ref.Selector = authority.Selector
	resolved, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return fmt.Errorf("authority source is unavailable or invalid: %w", err)
	}
	reading, err := evidence.Select(resolved, authority.Selector)
	if err != nil {
		return err
	}
	if reading.Kind != evidence.ReadingScalar || reading.Scalar.Type != "string" || reading.Scalar.String == nil {
		return admissionFault("quote-not-verbatim", "authority.selector",
			fmt.Sprintf("the authority's selector reads no text at %q, so the quote cannot be compared", authority.Selector.Pointer))
	}
	if *reading.Scalar.String != quote {
		return admissionFault("quote-not-verbatim", "quote",
			fmt.Sprintf("quote %q is not the authority's words %q", quote, *reading.Scalar.String))
	}
	return nil
}
