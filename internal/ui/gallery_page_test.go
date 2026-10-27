// Paged gallery transport must bound replies and retain entry capabilities.
// Selection and file observation counts belong to internal/gallery's tests.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
)

func TestGalleryPagesCarryNavigationAndFileCapabilities(t *testing.T) {
	host, session, root := galleryUI(t, &stubWorld{})
	path := filepath.Join(root, "page.txt")
	if err := os.WriteFile(path, []byte("page bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 27; i++ {
		e := addShown(t, root, path, fmt.Sprintf("Screen %02d", i), "")
		ids = append(ids, string(e.EntryID))
	}
	type row struct {
		gallery.Shown
		Cap string `json:"cap"`
	}
	type answer struct {
		Entries        []row
		Total, Offset  int
		Previous, Next *int
	}
	read := func(query string) answer {
		t.Helper()
		status, _, body := ask(t, host, "GET", "/api/gallery?project=team%2Fok"+query, "", session)
		var page answer
		if err := json.Unmarshal(body, &page); status != 200 || err != nil {
			t.Fatalf("%s: %d %v %s", query, status, err, body)
		}
		return page
	}
	first := read("&offset=0")
	if len(first.Entries) != 24 || first.Total != 27 || first.Previous != nil || first.Next == nil || *first.Next != 24 || first.Entries[0].ID != ids[26] {
		t.Fatalf("first page: %+v", first)
	}
	entry := first.Entries[0]
	file := "/api/gallery/file?project=team%2Fok&id=" + entry.ID + "&cap=" + entry.Cap
	status, _, body := ask(t, host, "GET", file, "", session)
	if status != 200 || string(body) != "page bytes" {
		t.Fatalf("page capability: %d %s", status, body)
	}
	second := read("&offset=24")
	if len(second.Entries) != 3 || second.Total != 27 || second.Next != nil || second.Previous == nil || *second.Previous != 0 || second.Entries[0].ID != ids[2] {
		t.Fatalf("second page: %+v", second)
	}
	filtered := read("&offset=0&search=Screen%2000&sort=title&kind=text")
	if filtered.Total != 1 || filtered.Entries[0].ID != ids[0] {
		t.Fatalf("filter across pages: %+v", filtered)
	}
	for _, suffix := range []string{"&offset=0&offset=0", "&offset=-1", "&offset=999999999999999999999999", "&offset=invalid", "&offset=0&sort=invalid", "&offset=0&kind=invalid", "&offset=0&search=" + strings.Repeat("x", 513), "&offset=0&search=%FF", "&offset=0&search=%ZZ", "&offset=0;search=ignored", "&offset=0&path=" + path} {
		status, _, body := ask(t, host, "GET", "/api/gallery?project=team%2Fok"+suffix, "", session)
		if status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", suffix, status, body)
		}
	}
	status, _, body = ask(t, host, "GET", file+"&offset=0", "", session)
	if status != http.StatusBadRequest || string(body) == "page bytes" {
		t.Fatalf("file accepted page parameters: %d %s", status, body)
	}
}

func TestGalleryPagePassesRequestCancellationToObservation(t *testing.T) {
	_, _, root := galleryUI(t, &stubWorld{})
	shown(t, root, "cancel.txt", []byte("media"))
	p := galleryProject(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "/api/gallery?offset=0", nil).WithContext(ctx)
	reply := httptest.NewRecorder()
	(&handler{}).galleryList(reply, request, p)
	if reply.Code != http.StatusInternalServerError || !strings.Contains(reply.Body.String(), "canceled") {
		t.Fatalf("cancelled page: %d %s", reply.Code, reply.Body.String())
	}
}
