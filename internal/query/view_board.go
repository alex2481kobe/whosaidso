package query

// Who is working on what: todo's in-flight tasks name each live claim's
// holder, when it was claimed and when it was last worked on, from the
// times the reducer records (reduce/activity.go). A claim with no admitted
// activity for longer than the project's stale_after is flagged a takeover
// candidate in attention. It is only flagged: releasing it takes the owner's
// approval, recorded as the receipt's authority. The read's clock is the
// caller's (ViewRequest.Now); without one nothing is judged stale.

import (
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// BoardClaim is one claim that holds an in-flight task. IdleFor and Stale are
// set only when the read was given the time.
type BoardClaim struct {
	Attempt        model.ID  `json:"attempt_id"`
	Holder         any       `json:"holder"`
	ClaimedAt      time.Time `json:"claimed_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	IdleFor        string    `json:"idle_for,omitempty"`
	Stale          *bool     `json:"stale,omitempty"`
}

// staleAfter is the project's threshold, the default for a Project built
// without a config.
func staleAfter(project store.Project) time.Duration {
	if project.Policy.StaleAfter > 0 {
		return project.Policy.StaleAfter
	}
	return store.DefaultStaleAfter
}

// boardClaims lists the claims that hold a task, and the attention each
// stale one is owed. A claim taken over holds nothing, so it is never a
// takeover candidate.
func boardClaims(s reduce.Snapshot, d Detail, project store.Project, now time.Time) ([]BoardClaim, []Attention) {
	claims, notes := []BoardClaim{}, []Attention{}
	limit := staleAfter(project)
	for _, a := range holding(s, reduce.Ident{Project: d.Ref.Project, ID: d.Ref.RecordID}) {
		c := BoardClaim{Attempt: a.Key.Attempt, Holder: actor(a.Actor), ClaimedAt: a.StartedAt, LastActivityAt: a.LastActivityAt}
		if !now.IsZero() {
			idle := now.Sub(a.LastActivityAt).Truncate(time.Minute)
			stale := idle > limit
			c.IdleFor, c.Stale = idle.String(), &stale
			if stale {
				notes = append(notes, Attention{Kind: "stale-claim", Ref: d.Ref, Label: d.Label,
					Reason: fmt.Sprintf("attempt %s held by %s: no admitted activity for %s, over stale_after %s; a takeover candidate, never released by itself: "+
						"with the owner's approval, release it (whosaidso help accept) or take it over", a.Key.Attempt, actorName(a.Actor), idle, limit)})
			}
		}
		claims = append(claims, c)
	}
	return claims, notes
}
