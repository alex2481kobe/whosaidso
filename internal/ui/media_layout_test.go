// Media states share a centered overlay and fill the preview area. This checks
// the embedded styling contract; browser layout and native controls need paint review.
package ui

import (
	"strings"
	"testing"
)

func TestGalleryLoadingOverlaysEdgeToEdgeMedia(t *testing.T) {
	raw, err := files.ReadFile("ui-gallery.css")
	if err != nil {
		t.Fatal(err)
	}
	rules := string(raw)
	rule := func(selector string) string {
		_, tail, ok := strings.Cut(rules, selector+" {")
		if !ok {
			t.Fatalf("missing media rule %s", selector)
		}
		body, _, _ := strings.Cut(tail, "}")
		return body
	}
	loading := rule(".gallery-load-status")
	for _, property := range []string{"position:absolute", "inset:0", "display:grid", "place-content:center"} {
		if !strings.Contains(loading, property) {
			t.Fatalf("loading must overlay the media without adding a strip: missing %s", property)
		}
	}
	media := rule(".gallery-media img,.gallery-media video")
	for _, property := range []string{"width:100%", "height:100%", "object-fit:cover"} {
		if !strings.Contains(media, property) {
			t.Fatalf("media must fill its card: missing %s", property)
		}
	}
	raw, err = files.ReadFile("ui.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "body>.theme-control") {
		t.Fatal("a separate floating theme box reserves a redundant row")
	}
}
