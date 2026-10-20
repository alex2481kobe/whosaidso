// The import source hash must not change for lists with nothing skipped, so
// seals written before skips existed still verify; a skip must change it, and
// only a known reason may be sealed.
package model

import "testing"

func TestGallerySourceKeepsLegacyEncodingWhenNothingIsSkipped(t *testing.T) {
	a, b := HashBytes([]byte("a")), HashBytes([]byte("b"))
	want := HashBytes([]byte(`["` + string(a) + `","` + string(b) + `"]`))
	if got := GallerySource([]Digest{a, b}); got != want {
		t.Errorf("source of a list with no skips changed: %s != %s", got, want)
	}
	if got := GallerySource([]Digest{}); got != HashBytes([]byte(`[]`)) {
		t.Errorf("empty source changed: %s", got)
	}
	skip := GalleryImportSkip{LegacyID: "gone", Path: "gone.png", Title: "Lost", Reason: "file missing"}
	with := GallerySource([]Digest{a, b}, skip)
	if with == want {
		t.Error("a skipped entry does not change the source")
	}
	skip.Path = "elsewhere.png"
	if GallerySource([]Digest{a, b}, skip) == with {
		t.Error("the skipped entry's path is not covered by the source")
	}
}

func TestGalleryImportSkipNeedsAKnownReason(t *testing.T) {
	seal := func(reason string) error {
		skip := GalleryImportSkip{LegacyID: "gone", Path: "gone.png", Title: "Lost", Reason: reason}
		_, err := EncodeEvent(&GalleryImport{Source: GallerySource([]Digest{}, skip), Inventory: []Digest{}, Entries: []ID{}, Skipped: []GalleryImportSkip{skip}})
		return err
	}
	if err := seal("file missing"); err != nil {
		t.Fatalf("control: %v", err)
	}
	if seal("because") == nil {
		t.Error("an unknown skip reason was sealed")
	}
}
