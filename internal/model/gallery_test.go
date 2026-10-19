// Gallery schema tests keep incomplete pins and unidentified requirements out
// of every entrance. Content identity excludes the retrieval locator.
package model

import (
	"strings"
	"testing"
	"time"
)

func TestGallerySchemaAndContentIdentity(t *testing.T) {
	add := GalleryAdd{AddedAt: time.Now(), EntryID: ID("00000000000000000000000001"), File: GalleryFile{SHA256: HashBytes([]byte("video")), Length: 5, Path: "clip.mp4"}, Title: "Rendered pose", Actor: Actor{ID: "contributor"}, Tasks: []RecordRef{}}
	if _, err := EncodeEvent(&add); err != nil {
		t.Fatalf("control: %v", err)
	}
	for _, change := range []func(*GalleryAdd){
		func(e *GalleryAdd) { e.Title = "two\nlines" }, func(e *GalleryAdd) { e.Title = strings.Repeat("t", 121) }, func(e *GalleryAdd) { e.Note = strings.Repeat("n", 1001) }, func(e *GalleryAdd) { e.File.SHA256 = "" }, func(e *GalleryAdd) { e.File.Path = "" }, func(e *GalleryAdd) { e.Actor = Actor{} },
	} {
		bad := add
		change(&bad)
		if _, err := EncodeEvent(&bad); err == nil {
			t.Fatalf("invalid gallery event encoded: %+v", bad)
		}
	}
	ref := RecordRef{Project: "test/gallery", RecordID: add.EntryID, Revision: 1}
	require := GalleryRequire{Task: ref, CriterionID: add.EntryID, CriterionRevision: 1, EntryID: add.EntryID, Approver: Actor{ID: "reviewer"}}
	if _, err := EncodeEvent(&require); err != nil {
		t.Fatalf("requirement control: %v", err)
	}
	require.Approver = Actor{UnknownReason: "not supplied"}
	if _, err := EncodeEvent(&require); err == nil {
		t.Fatal("unknown approver encoded")
	}
	reject := GalleryReject{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: Actor{ID: "reviewer"}, Note: "pose differs"}
	if _, err := EncodeEvent(&reject); err != nil {
		t.Fatal(err)
	}
	reject.Note = " "
	if _, err := EncodeEvent(&reject); err == nil {
		t.Fatal("blank rejection note encoded")
	}
	other := add.File
	other.Path = "another/locator.mp4"
	if !add.File.SameContent(other) {
		t.Fatal("locator changed identity")
	}
	other.Length++
	if add.File.SameContent(other) {
		t.Fatal("length mismatch matched")
	}
	other = add.File
	other.SHA256 = HashBytes([]byte("other"))
	if add.File.SameContent(other) {
		t.Fatal("hash mismatch matched")
	}
	if (GalleryFile{}).SameContent(GalleryFile{}) {
		t.Fatal("unknown pins matched")
	}
}
