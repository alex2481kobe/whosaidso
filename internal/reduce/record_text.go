// A record's semantic text is its kind's authored statement. Formatting,
// truncation and searching that text belong to query and presentation.
package reduce

import "github.com/alex2481kobe/whosaidso/internal/model"

func (r Record) SemanticText() (string, bool) {
	switch r.Kind {
	case model.Task:
		return r.Task.Intent, true
	case model.Claim:
		return r.Claim.Assertion, true
	case model.Decision:
		return r.Decision.Question, true
	case model.Instrument:
		return r.Instrument.QuestionAnswered, true
	}
	return "", false
}
