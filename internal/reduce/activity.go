package reduce

// When a claim was last worked on: each admitted act that names an attempt
// moves its LastActivityAt to the admitting bundle's recorded_at, the
// ledger's own clock. What counts is the attempt's start, its runs' starts and
// seals, and its receipt (task_events.go and events.go). A hold or amendment
// names the task, not the attempt, and anyone may record one, so it is no one
// holder's activity. Whether a claim is stale is a read's question, against a
// threshold and a time the reader supplies (internal/query).

import "time"

// touchAttempt records activity on one attempt at the bundle's time.
func (s *state) touchAttempt(key AttemptKey, at time.Time) {
	a, ok := s.attempts[key]
	if !ok || !at.UTC().After(a.LastActivityAt) {
		return
	}
	a.LastActivityAt = at.UTC()
	put(s, s.attempts, key, a)
}
