// This file pins the complete agent-facing JSON answers on the synthetic
// 1,000-bundle fixture, so a performance change can prove it left every answer
// byte-identical. Timing loops and fixture construction do not belong here.
package benchmarks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/store"
)

// The golden holds one sha-256 per answer, generated from main before the
// sort-once/describe-once/copy-on-write changes. The fixture stamps wall-clock
// times into packets, bundles and envelopes, and places its project under a
// temporary path, so the answer bytes differ between builds only in those
// spellings and in digests computed over them. normalizeAnswer replaces exactly
// those: timestamps and the durations measured between them by placeholders, the project and root by names, and each
// distinct digest by its first-appearance ordinal (so equal digests stay equal
// and distinct ones stay distinct). Everything else — order, counts, statuses,
// ids, reasons, UNKNOWNs — is compared byte for byte.
//
// Blind spot: two answers that differ only in which timestamp sits where, or
// only in digest bytes with the same equality pattern, hash the same. The raw
// answers are also written, unnormalized, to DATUM_ANSWERS_DUMP when set, so a
// change can be compared byte for byte on one retained fixture
// (DATUM_BENCH_ROOT) as well.
const answersGolden = "testdata/answers-n1000.golden"

var (
	timestampPattern = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})"`)
	digestPattern    = regexp.MustCompile(`[0-9a-f]{64}`)
	durationPattern  = regexp.MustCompile(`"nanoseconds": -?\d+,(\s*)"text": "[^"]*"`)
)

func goldenAnswers(t *testing.T, f *fixture) map[string][]byte {
	t.Helper()
	observedAt := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	observed := query.Observation{ObservedAt: known(observedAt), Head: unknown[model.GitHead](), Dirty: unknown[bool]()}
	requests := []struct {
		name    string
		request query.Request
	}{
		{"show", query.Request{Command: "show"}},
		{"show-one", query.Request{Command: "show", ID: f.Task.RecordID}},
		{"todo", query.Request{Command: "todo"}},
		{"now", query.Request{Command: "now"}},
		{"context", query.Request{Command: "context"}},
		{"continue", query.Request{Command: "continue", ID: f.Task.RecordID, Observed: &observed}},
	}
	project, err := store.Discover(f.Project.Root)
	must(t, err)
	out := map[string][]byte{}
	for _, r := range requests {
		answer, err := query.Read(project, r.request)
		must(t, err)
		var buf bytes.Buffer
		must(t, query.RenderJSON(&buf, answer))
		out[r.name] = buf.Bytes()
	}
	return out
}

func normalizeAnswer(raw []byte, project store.Project) []byte {
	text := string(raw)
	text = strings.ReplaceAll(text, project.Root, "<root>")
	text = strings.ReplaceAll(text, string(project.ID), "<project>")
	text = timestampPattern.ReplaceAllString(text, `"<time>"`)
	text = durationPattern.ReplaceAllString(text, `"nanoseconds": "<ns>",$1"text": "<duration>"`)
	ordinals := map[string]string{}
	text = digestPattern.ReplaceAllStringFunc(text, func(d string) string {
		if _, ok := ordinals[d]; !ok {
			ordinals[d] = fmt.Sprintf("<digest-%d>", len(ordinals)+1)
		}
		return ordinals[d]
	})
	return []byte(text)
}

func answerDigests(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	answers := goldenAnswers(t, f)
	dump := os.Getenv("DATUM_ANSWERS_DUMP")
	out := map[string]string{}
	for name, raw := range answers {
		if dump != "" {
			put(t, filepath.Join(dump, name+".json"), raw)
		}
		sum := sha256.Sum256(normalizeAnswer(raw, f.Project))
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

func readGolden(t *testing.T) map[string]string {
	t.Helper()
	file, err := os.Open(answersGolden)
	must(t, err)
	defer file.Close()
	out := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			t.Fatalf("malformed golden line %q", scanner.Text())
		}
		out[fields[0]] = fields[1]
	}
	must(t, scanner.Err())
	return out
}

// TestAnswersMatchMainGolden: every pinned answer on the 1k fixture hashes to
// the value main produced. DATUM_WRITE_GOLDEN=1 rewrites the golden; only do
// that on the commit the golden is meant to describe.
func TestAnswersMatchMainGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and reads the 1,000-bundle fixture")
	}
	f := getFixture(t, 1000)
	got := answerDigests(t, f)
	if os.Getenv("DATUM_WRITE_GOLDEN") == "1" {
		var buf bytes.Buffer
		for _, name := range []string{"continue", "context", "now", "show", "show-one", "todo"} {
			fmt.Fprintf(&buf, "%s %s\n", name, got[name])
		}
		put(t, answersGolden, buf.Bytes())
		return
	}
	want := readGolden(t)
	if len(want) != len(got) {
		t.Fatalf("golden has %d answers, test produced %d", len(want), len(got))
	}
	for name, digest := range got {
		if want[name] != digest {
			t.Errorf("%s answer changed: normalized sha256 %s, main produced %s", name, digest, want[name])
		}
	}
}

// TestAnswerNormalizationIsStable is the control for the golden: two fixtures
// built at different times under different roots must normalize identically,
// or the golden would compare build noise instead of answers.
func TestAnswerNormalizationIsStable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two fixtures")
	}
	a := buildFixture(t, filepath.Join(t.TempDir(), "a"), 30, false)
	time.Sleep(2 * time.Millisecond)
	b := buildFixture(t, filepath.Join(t.TempDir(), "b"), 30, false)
	x, y := answerDigests(t, a), answerDigests(t, b)
	for name := range x {
		if x[name] != y[name] {
			t.Errorf("%s: normalization leaves build noise (%s vs %s)", name, x[name], y[name])
		}
	}
	// A real difference must survive normalization.
	raw := goldenAnswers(t, a)["show"]
	changed := bytes.Replace(raw, []byte(`"IN FLIGHT"`), []byte(`"BLOCKED"`), 1)
	if bytes.Equal(changed, raw) {
		t.Fatal("control mutation did not apply; pick a status the fixture renders")
	}
	if bytes.Equal(normalizeAnswer(changed, a.Project), normalizeAnswer(raw, a.Project)) {
		t.Fatal("normalization erased a status change")
	}
}
