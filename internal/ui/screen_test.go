// Project discovery must be immediate, and screen transport must page before
// exporting ledger payloads. These fixtures never use the repository ledger.
package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectSwitcherCanListWithoutBuildingStatus(t *testing.T) {
	world := &stubWorld{projects: available()}
	host, token := testUI(t, world)
	code, _, body := ask(t, host, "GET", "/api/projects?summary=0", "", enter(t, host, token))
	if code != 200 || !strings.Contains(string(body), `"available":true`) || world.reads.Load() != 0 || strings.Contains(string(body), `"watermark"`) {
		t.Fatalf("lean discovery: %d %s reads %d", code, body, world.reads.Load())
	}
}

func TestScreenTransportBoundsResponsesAndRefusesMalformedFilters(t *testing.T) {
	host, session, root := galleryUI(t, &stubWorld{})
	shown(t, root, "screen.png", []byte("media"))
	code, _, body := ask(t, host, "GET", "/api/screen?project=team%2Fok&screen=history&offset=0", "", session)
	var page struct {
		Rows  []json.RawMessage
		Total int
	}
	if err := json.Unmarshal(body, &page); code != 200 || err != nil || len(page.Rows) == 0 || len(page.Rows) > 24 || strings.Contains(string(body), `"data":`) {
		t.Fatalf("screen: %d %s %v", code, body, err)
	}
	for _, suffix := range []string{"&offset=-1", "&offset=bad", "&offset=0&offset=1", "&search=%FF", "&search=%ZZ", "&sort=invalid", "&kind=TASK", "&path=ignored", "&status_counts=bad", "&status_counts=1"} {
		code, _, body := ask(t, host, "GET", "/api/screen?project=team%2Fok&screen=history"+suffix, "", session)
		if code != 400 {
			t.Fatalf("invalid filter %s: %d %s", suffix, code, body)
		}
	}
}

func TestCaptureTimeTransportRejectsAmbiguousRequests(t *testing.T) {
	host, session, root := galleryUI(t, &stubWorld{})
	id, _ := shown(t, root, "time.png", []byte("media"))
	base := "/api/times?project=team%2Fok&id=" + id
	code, _, body := ask(t, host, "GET", base, "", session)
	var answer struct {
		Result string
		Times  map[string]any
	}
	if err := json.Unmarshal(body, &answer); code != 200 || err != nil || answer.Result != "UNKNOWN" || len(answer.Times) != 0 {
		t.Fatalf("unknown record control: %d %s %v", code, body, err)
	}
	for _, suffix := range []string{"&id=" + id, "&search=ignored", "&id=%ZZ", "&id=bad;ignored=true"} {
		code, _, body := ask(t, host, "GET", base+suffix, "", session)
		if code != 400 {
			t.Fatalf("ambiguous capture time: %d %s", code, body)
		}
	}
}
