package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"datum/internal/model"
)

// Compare every leaf and empty container, including full field paths. The text
// parser is test-only; the production renderer has no domain-specific branches.
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

func textLeaves(t *testing.T, text string) map[string]string {
	t.Helper()
	leaves := map[string]string{}
	var path []string
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		body := strings.TrimLeft(line, " ")
		depth := (len(line) - len(body)) / 2
		var label, rest string
		if strings.HasPrefix(body, `"`) {
			decoder := json.NewDecoder(strings.NewReader(body))
			var key string
			if err := decoder.Decode(&key); err != nil {
				t.Fatal(err)
			}
			label, rest = body[:decoder.InputOffset()], body[decoder.InputOffset():]
		} else {
			at := strings.IndexByte(body, ':')
			if at < 0 {
				t.Fatalf("expected field delimiter, got %q", body)
			}
			label, rest = body[:at], body[at:]
		}
		path = append(path[:depth], label)
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		if rest != "" {
			if !json.Valid([]byte(rest)) {
				t.Fatalf("expected literal JSON leaf without editorial text, got %q", rest)
			}
			leaves[strings.Join(path, "\n")] = rest
		}
	}
	return leaves
}

func TestTextAndJSONCarryExactlyTheSameFacts(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	capturePacket(t, p, 2)
	for _, command := range []string{"show", "history", "task todo", "intake pending"} {
		a := readAnswer(t, p, command, "")
		var exported, rendered bytes.Buffer
		if err := RenderJSON(&exported, a); err != nil {
			t.Fatal(err)
		}
		if err := RenderText(&rendered, a); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jsonLeaves(t, exported.Bytes()), textLeaves(t, rendered.String())) {
			t.Fatalf("%s text and JSON disagree on a field/value; both must render the complete answer", command)
		}
		var again bytes.Buffer
		if err := RenderText(&again, a); err != nil || again.String() != rendered.String() {
			t.Fatalf("same answer must render deterministically, got error %v", err)
		}
	}
}

func TestTextPreservesLargeNumbersAndQuotesAuthoredControlCharacters(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	a := readAnswer(t, p, "show", testID(1))
	a.Watermark.Sequence = 9007199254740993
	a.Records[0].Fact.Task.Intent = "line one\nstatus: CLOSED\x1b[2J"
	var rendered bytes.Buffer
	if err := RenderText(&rendered, a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), `"sequence": 9007199254740993`) || strings.Contains(rendered.String(), "\x1b") || strings.Contains(rendered.String(), "\nstatus: CLOSED") {
		t.Fatalf("text must preserve uint64 precision and quote authored terminal controls, got %s", rendered.String())
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderersPropagateOutputAndEncodingErrors(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	a := readAnswer(t, p, "show", testID(1))
	for _, render := range []func(io.Writer, Answer) error{RenderText, RenderJSON} {
		if err := render(io.Discard, a); err != nil {
			t.Fatalf("control valid answer must render: %v", err)
		}
		if err := render(failedWriter{}, a); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected writer failure, got %v; truncated output cannot be reported as success", err)
		}
		broken := a
		broken.History = []Event{{Event: model.Event{Type: "task.create", Data: []byte("invalid JSON")}}}
		if err := render(io.Discard, broken); err == nil {
			t.Fatal("expected encoding error for malformed event bytes, got success")
		}
	}
}
