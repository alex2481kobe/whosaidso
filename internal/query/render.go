package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderJSON exports exactly the answer, with no filesystem or reducer access.
func RenderJSON(w io.Writer, answer Answer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(answer)
}

// RenderText is a lossless field outline of the JSON answer. Keeping this
// renderer deliberately small avoids a second set of domain rules. Strings are
// quoted so authored newlines/control characters cannot impersonate fields.
// Numbers retain their decimal bytes, including ledger positions above 2^53.
// This file stays below the usual line range because formatting is its only job.
func RenderText(w io.Writer, answer Answer) error {
	var encoded bytes.Buffer
	if err := RenderJSON(&encoded, answer); err != nil {
		return err
	}
	decoder := json.NewDecoder(&encoded)
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var output strings.Builder
	outline(&output, "answer", value, 0)
	_, err := io.WriteString(w, output.String())
	return err
}

func outline(w *strings.Builder, label string, value any, depth int) {
	indent := strings.Repeat("  ", depth)
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			fmt.Fprintf(w, "%s%s: {}\n", indent, label)
			return
		}
		fmt.Fprintf(w, "%s%s:\n", indent, label)
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			// JSON quoting also protects authored map keys such as tool knobs.
			quoted, _ := json.Marshal(key)
			outline(w, string(quoted), v[key], depth+1)
		}
	case []any:
		if len(v) == 0 {
			fmt.Fprintf(w, "%s%s: []\n", indent, label)
			return
		}
		fmt.Fprintf(w, "%s%s:\n", indent, label)
		for i, item := range v {
			outline(w, fmt.Sprintf("[%d]", i), item, depth+1)
		}
	default:
		encoded, _ := json.Marshal(value) // decoder produced JSON-only values
		fmt.Fprintf(w, "%s%s: %s\n", indent, label, encoded)
	}
}
