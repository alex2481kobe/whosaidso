// An entry maker cannot fulfill a visual task gate with their own approval.
// Their rejection can still record a defect in their work.
package reduce

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryMakerCannotApproveTheirOwnTaskGate(t *testing.T) {
	for _, verdict := range []string{"approve", "reject"} {
		t.Run(verdict, func(t *testing.T) {
			l := goodLedger(t)
			l.add(t, &model.AttemptTerminal{Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Outcome: model.AttemptSuccess, Reason: "render delivered", NextAction: "visual review", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")}})
			maker := model.Actor{ID: "maker"}
			add := &model.GalleryAdd{EntryID: newID("GAY1"), AddedAt: baseTime, File: model.GalleryFile{SHA256: newDigest("video"), Length: 5, Path: "clip.mp4"}, Title: "Render", Actor: maker, Tasks: []model.RecordRef{}}
			galleryFact(t, l, maker, add)
			galleryFact(t, l, model.Actor{ID: "coordinator"}, &model.GalleryRequire{Task: ref(newID("TSKA"), 1), CriterionID: newID("ACCA"), CriterionRevision: 1, EntryID: add.EntryID, Approver: maker})
			if verdict == "approve" {
				galleryFact(t, l, maker, &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: maker})
			} else {
				galleryFact(t, l, maker, &model.GalleryReject{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: maker, Note: "pose differs"})
				if got := mustReplay(t, l.bundles()).Gallery()[0].Verdict; got != "rejected" {
					t.Fatalf("maker rejection lost: %s", got)
				}
			}
			applyErr, replayErr, _ := closeBothWays(t, l, closeSuccess(newID("TSKA"), 1))
			for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
				if err == nil {
					t.Errorf("%s allowed maker's %s to satisfy task gate", name, verdict)
				}
			}
		})
	}
}
