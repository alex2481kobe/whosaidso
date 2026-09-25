package main

// Help examples that are contracts are held to the code that reads them: the
// whosaidso.toml example parses as a project, the producer report example
// decodes as write.ProducerReport, and the measurement envelope example is
// read by evidence.Select with the metadata the guide says it states. The
// guide also names the close vocabulary and no longer contradicts --blob.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

func TestHelpContractExamplesAreWhatTheCodeReads(t *testing.T) {
	whole, _ := helpFor("")

	var config []string
	for _, line := range strings.Split(whole, "\n") {
		if m := regexp.MustCompile(`^  ((id|ledger) = '[^']*')`).FindStringSubmatch(line); m != nil {
			config = append(config, m[1])
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(strings.Join(config, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := store.Discover(root); len(config) != 2 || err != nil || p.ID != "animation/toy" {
		t.Fatalf("the guide's whosaidso.toml example must parse as a project: %q %v", config, err)
	}

	report := regexp.MustCompile(`(?s)(\{"version":1,.*?"conditions_observed":\{\}\})`).FindString(whole)
	d := json.NewDecoder(strings.NewReader(report))
	d.DisallowUnknownFields()
	var r write.ProducerReport
	if err := d.Decode(&r); err != nil || r.Version != 1 || len(r.Outputs) != 1 || r.Outputs[0].Path != "result.json" {
		t.Fatalf("the guide's producer report must decode as the one run reads: %q %v", report, err)
	}

	// Each example is parsed exactly as printed: no whitespace is normalized,
	// so a line break inside a JSON string fails here as it fails a reader.
	envelope := regexp.MustCompile(`(?s)\{"value": 0\.75.*?"one step"\}`).FindString(whole)
	data := []byte(`{"absolute_error":` + envelope + `}`)
	reading, err := evidence.Select(evidence.ResolvedArtifact{Bytes: data, SHA256: model.HashBytes(data)}, model.Selector{Kind: "json-pointer", Pointer: "/absolute_error"})
	if err != nil || reading.Kind != evidence.ReadingScalar || reading.Unit.Value == nil || *reading.Unit.Value != "world units" ||
		reading.Denominator.Value == nil || *reading.Denominator.Value != "one step" || reading.Population.State != model.Known {
		t.Fatalf("the guide's measurement envelope must read as a scalar with its metadata: %s %+v %v", data, reading, err)
	}

	for _, want := range []string{"delivery_witness_refs", "cancelled, withdrawn and waived", "only success satisfies a task-success prerequisite",
		"reason=awaiting-acceptance", "whosaidso template blocker.clear", "WHOSAIDSO_RUN_DIR", "WHOSAIDSO_RUN_REPORT", "order is its zero-based"} {
		if !strings.Contains(whole, want) {
			t.Errorf("the guide must say %q", want)
		}
	}
	if bytes.Contains([]byte(whole), []byte("carries no blobs")) {
		t.Error("check admission must not deny the --blob it documents")
	}
}
