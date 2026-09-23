package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"datum/internal/model"
)

// Compare every leaf and empty container, including full field paths.
func jsonLeaves(t *testing.T, data []byte) map[string]string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	leaves := map[string]string{}
	var visit func(any, string)
	visit = func(v any, path string) {
		switch node := v.(type) {
		case map[string]any:
			if len(node) > 0 {
				for key, child := range node {
					encoded, _ := json.Marshal(key)
					visit(child, path+"\n"+string(encoded))
				}
				return
			}
		case []any:
			if len(node) > 0 {
				for i, child := range node {
					visit(child, path+"\n["+strconv.Itoa(i)+"]")
				}
				return
			}
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		leaves[path] = string(encoded)
	}
	visit(value, "answer")
	return leaves
}

// The brief keeps uint64 precision and never lets authored text break a line.
func TestBriefPreservesLargeNumbersAndQuotesAuthoredControlCharacters(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	a := showOf(t, p, testID(1))
	a.Watermark.Sequence = 9007199254740993
	a.Records[0].Fact.Task.Intent = "line one\nstatus: CLOSED\x1b[2J"
	var rendered bytes.Buffer
	if err := RenderViewBrief(&rendered, a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "sequence 9007199254740993") || strings.Contains(rendered.String(), "\x1b") || strings.Contains(rendered.String(), "\nstatus: CLOSED") {
		t.Fatalf("the brief must preserve uint64 precision and quote authored terminal controls, got %s", rendered.String())
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderersPropagateOutputAndEncodingErrors(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	a := historyOf(t, p, "")
	for _, render := range []func(io.Writer, ViewAnswer) error{RenderViewBrief, RenderViewJSON} {
		if err := render(io.Discard, a); err != nil {
			t.Fatalf("control valid answer must render: %v", err)
		}
		if err := render(failedWriter{}, a); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected writer failure, got %v; truncated output cannot be reported as success", err)
		}
		broken := *a
		broken.Events = []Event{{Event: model.Event{Type: "task.create", Data: []byte("invalid JSON")}}}
		if err := render(io.Discard, &broken); err == nil {
			t.Fatal("expected encoding error for malformed event bytes, got success")
		}
	}
}
