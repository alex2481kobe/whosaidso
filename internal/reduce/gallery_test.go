package reduce

// Gallery fixtures cover actor/hash identity, retraction and refusal on replay.
// They contain no machine files: local availability is an admission concern.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func galleryFact(t *testing.T, l *ledgerBuilder, by model.Actor, e model.TypedEvent) model.Bundle {
	t.Helper()
	packet := newID(fmt.Sprintf("GP%d", l.seq+1+l.names))
	review := &model.ReviewAdmit{Outcome: "accepted", Actor: model.Actor{ID: "coordinator"}, Reason: "fixture reviewed", Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest(string(packet))}}, EventPackets: []model.ID{packet}, Authors: map[model.ID]model.Actor{packet: by}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet: knownAt(baseTime)}}
	return l.add(t, e, review)
}

func galleryLedger(t *testing.T) (*ledgerBuilder, *model.GalleryAdd, *model.GalleryRequire) {
	t.Helper()
	l := goodLedger(t)
	l.add(t, &model.AttemptTerminal{Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Outcome: model.AttemptSuccess, Reason: "render delivered", NextAction: "visual review", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")}})
	add := &model.GalleryAdd{EntryID: newID("GAY1"), AddedAt: baseTime, File: model.GalleryFile{SHA256: newDigest("video"), Length: 5, Path: "clip.mp4"}, Title: "Render", Actor: model.Actor{ID: "contributor"}, Tasks: []model.RecordRef{}}
	galleryFact(t, l, add.Actor, add)
	req := &model.GalleryRequire{Task: ref(newID("TSKA"), 1), CriterionID: newID("ACCA"), CriterionRevision: 1, EntryID: add.EntryID, Approver: model.Actor{ID: "reviewer"}}
	galleryFact(t, l, model.Actor{ID: "coordinator"}, req)
	return l, add, req
}

func TestGalleryClosureNeedsAdmittedActorAndHashApproval(t *testing.T) {
	for _, mode := range []string{"control", "unapproved", "wrong-actor", "rejected", "retracted", "other-entry"} {
		t.Run(mode, func(t *testing.T) {
			l, add, _ := galleryLedger(t)
			if mode != "unapproved" {
				actor := model.Actor{ID: "reviewer"}
				if mode == "wrong-actor" {
					actor.ID = "other-reviewer"
				}
				id := add.EntryID
				if mode == "other-entry" {
					other := *add
					other.EntryID = newID("GAY2")
					galleryFact(t, l, other.Actor, &other)
					id = other.EntryID
				}
				galleryFact(t, l, actor, &model.GalleryApprove{EntryID: id, SHA256: add.File.SHA256, Actor: actor})
			}
			if mode == "rejected" {
				by := model.Actor{ID: "reviewer"}
				galleryFact(t, l, by, &model.GalleryReject{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: by, Note: "pose disagrees"})
			}
			if mode == "retracted" {
				galleryFact(t, l, model.Actor{ID: "contributor"}, &model.GalleryRetract{EntryID: add.EntryID})
			}
			applyErr, replayErr, s := closeBothWays(t, l, closeSuccess(newID("TSKA"), 1))
			for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
				if mode == "control" {
					if err != nil {
						t.Fatalf("%s control: %v", name, err)
					}
				} else {
					f := wantFault(t, err, CodeInvalidTransition)
					if !strings.Contains(f.Detail, string(add.EntryID)) || !strings.Contains(f.Detail, "reviewer") {
						t.Fatalf("missing actionable refusal: %v", err)
					}
				}
			}
			if mode == "control" {
				p, _ := s.Task(ident(ref(newID("TSKA"), 1)))
				if p.Status != StatusClosed {
					t.Fatalf("control not closed: %+v", p)
				}
			}
		})
	}
}

func TestGalleryRefusesInvalidTransitionsOnApplyAndReplay(t *testing.T) {
	for _, mode := range []string{"wrong-hash", "unknown-actor", "forged-actor", "duplicate-entry", "bad-criterion", "unknown-entry", "duplicate-requirement", "duplicate-import", "unadmitted-import-entry"} {
		t.Run(mode, func(t *testing.T) {
			l, add, req := galleryLedger(t)
			by := model.Actor{ID: "reviewer"}
			var e model.TypedEvent = &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: by}
			switch mode {
			case "wrong-hash":
				e.(*model.GalleryApprove).SHA256 = newDigest("different video")
			case "unknown-actor":
				by = model.Actor{UnknownReason: "not supplied"}
				e.(*model.GalleryApprove).Actor = by
			case "forged-actor":
				by.ID = "impostor"
			case "duplicate-entry":
				e = add
				by = add.Actor
			case "bad-criterion":
				r := *req
				r.CriterionID = newID("N0NE")
				e = &r
				by.ID = "coordinator"
			case "unknown-entry":
				e.(*model.GalleryApprove).EntryID = newID("N0NE")
			case "duplicate-requirement":
				e = req
				by.ID = "coordinator"
			case "duplicate-import":
				add2 := *add
				add2.EntryID = newID("GAY2")
				add2.ImportID = "legacy-entry"
				galleryFact(t, l, add.Actor, &add2)
				add3 := add2
				add3.EntryID = newID("GAY3")
				e = &add3
				by = add.Actor
			case "unadmitted-import-entry":
				e = &model.GalleryImport{Source: newDigest("legacy"), Entries: []model.ID{newID("N0NE")}}
			}
			prefix := mustReplay(t, l.bundles())
			b := galleryFact(t, l, by, e)
			if _, err := Apply(prefix, b); err == nil {
				t.Fatal("invalid event accepted by Apply")
			}
			if _, err := Replay(l.bundles()); err == nil {
				t.Fatal("invalid event accepted by Replay")
			}
		})
	}
}

func galleryRestoreLedger(t *testing.T) []model.Bundle {
	l, add, _ := galleryLedger(t)
	by := model.Actor{ID: "reviewer"}
	galleryFact(t, l, by, &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: by})
	galleryFact(t, l, by, &model.GalleryReject{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: by, Note: "pose disagrees"})
	galleryFact(t, l, by, &model.GalleryRetract{EntryID: add.EntryID})
	imported := *add
	imported.EntryID = newID("GAY2")
	imported.ImportID = "legacy"
	galleryFact(t, l, imported.Actor, &imported)
	galleryFact(t, l, by, &model.GalleryImport{Source: newDigest("legacy-list"), Entries: []model.ID{imported.EntryID}})
	return l.bundles()
}
