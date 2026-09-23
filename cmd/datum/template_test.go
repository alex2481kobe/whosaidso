package main

// Tests that hold `datum template` to the model: every template decodes
// through the strict decoder once filled, an unfilled one never does, every
// enum and union member is one the model accepts, and every enum set and event
// type the model declares is known to the template tables.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

// templateFill is what a test author types for each placeholder kind. Every
// reference id is the same, so equal-reference rules (a clear's hold_ref, a
// seal's start link, a proof's claim) hold; minted ids stay distinct.
func templateFill(kind string) (any, bool) {
	values := map[string]any{
		"text": "filled", "actor-id": "someone", "id": "01M36A3HTZY2Z66JFRT31X44DP", "project": "test/project",
		"digest": string(model.HashBytes([]byte("x"))), "revision": json.Number("1"), "count": json.Number("1"),
		"integer": json.Number("0"), "number": json.Number("1"), "bool": false, "time": "2026-09-23T00:00:00Z",
		"path": "a/b.txt", "commit": "0123456789abcdef0123456789abcdef01234567", "media-type": "application/json",
		"pointer": "/a", "key": "k",
	}
	v, ok := values[kind]
	return v, ok
}

func templateKind(s string) (string, string, bool) {
	if !strings.HasPrefix(s, "<") || !strings.HasSuffix(s, ">") {
		return "", "", false
	}
	kind, hint, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">"), ": ")
	return kind, hint, true
}

// templateNode is the node at path, or nil.
func templateNode(root any, path []string) any {
	cur := root
	for _, part := range path {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[part]
		case []any:
			if len(node) == 0 {
				return nil
			}
			cur = node[0]
		default:
			return nil
		}
	}
	return cur
}

// templateAt navigates a decoded template to the object holding path's last key.
func templateAt(root any, path []string) (map[string]any, string, bool) {
	cur := root
	for _, part := range path[:len(path)-1] {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[part]
		case []any:
			if len(node) == 0 {
				return nil, "", false
			}
			cur = node[0]
		default:
			return nil, "", false
		}
	}
	parent, ok := cur.(map[string]any)
	return parent, path[len(path)-1], ok
}

// fillTemplate applies the choices (keyed by rendered path), prunes union
// branches the choice does not keep, keeps only the optional keys named, and
// replaces every remaining placeholder.
func fillTemplate(t *testing.T, eventType model.EventType, choices map[string]string, keep map[string]bool) []byte {
	t.Helper()
	data, notes, err := buildTemplate(eventType)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var tree []any
	if err := decoder.Decode(&tree); err != nil {
		t.Fatal(err)
	}
	body := tree[0].(map[string]any)["data"]
	for _, n := range notes {
		if n.Kind != "choose" {
			continue
		}
		object, ok := templateNode(body, n.Path).(map[string]any)
		if !ok {
			continue // under an optional key that was already removed
		}
		at := templatePath(append(append([]string(nil), n.Path...), n.Union.tag))
		if n.Union.tag == "" {
			at = templatePath(n.Path) + ".(one of)"
		}
		chosen := choices[at]
		if chosen == "" {
			chosen = n.Union.members[0]
		}
		if n.Union.tag != "" {
			object[n.Union.tag] = chosen
		}
		kept := map[string]bool{}
		for _, key := range n.Union.branches[chosen] {
			kept[key] = true
		}
		for _, keys := range n.Union.branches {
			for _, key := range keys {
				if !kept[key] {
					delete(object, key)
				}
			}
		}
	}
	for _, n := range notes {
		if n.Kind == "optional" && !keep[templatePath(n.Path)] {
			if parent, key, ok := templateAt(body, n.Path); ok {
				delete(parent, key)
			}
		}
	}
	var replace func(v any, path string) any
	replace = func(v any, path string) any {
		switch node := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, value := range node {
				filled := key
				if kind, _, ok := templateKind(key); ok {
					value, _ := templateFill(kind)
					filled = value.(string)
				}
				out[filled] = replace(value, strings.TrimPrefix(path+"."+key, "."))
			}
			return out
		case []any:
			for i := range node {
				node[i] = replace(node[i], path+"[0]")
			}
			return node
		case string:
			kind, hint, ok := templateKind(node)
			if !ok {
				return node
			}
			if kind == "one of" {
				if chosen, ok := choices[path]; ok {
					return chosen
				}
				return strings.Split(hint, " | ")[0]
			}
			value, ok := templateFill(kind)
			if !ok {
				t.Fatalf("%s: placeholder %q at %s has a kind no author could fill", eventType, node, path)
			}
			return value
		}
		return v
	}
	tree[0].(map[string]any)["data"] = replace(body, "")
	filled, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return filled
}

func decodeTemplate(data []byte) error {
	events, err := model.DecodeEvents(data)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := model.DecodeEvent(event); err != nil {
			return err
		}
	}
	return nil
}

func TestEveryTemplateDecodesOnceFilledAndNeverBefore(t *testing.T) {
	for _, event := range templateEvents {
		raw, _, err := buildTemplate(event.EventType())
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeTemplate(raw); err == nil {
			t.Errorf("%s: an unfilled template decodes, so it could be captured by accident", event.EventType())
		}
		if err := decodeTemplate(fillTemplate(t, event.EventType(), nil, nil)); err != nil {
			t.Errorf("%s: the filled template does not decode: %v", event.EventType(), err)
		}
	}
}

// Every member of every choice and enum must decode in some template, with the
// optional keys left out or kept one at a time. A member listed here that the
// model refuses everywhere is a table drifted from the schema.
func TestEveryChoiceMemberDecodesSomewhere(t *testing.T) {
	works := map[string]bool{}
	for _, event := range templateEvents {
		raw, notes, _ := buildTemplate(event.EventType())
		var optional []string
		for _, n := range notes {
			if n.Kind == "optional" {
				optional = append(optional, templatePath(n.Path))
			}
		}
		keeps := []map[string]bool{nil}
		for _, key := range optional {
			keeps = append(keeps, map[string]bool{key: true})
		}
		for _, loc := range templateChoiceLocations(t, raw, notes) {
			for _, member := range loc.members {
				id := loc.set + " " + member
				for _, keep := range keeps {
					if works[id] {
						break
					}
					works[id] = decodeTemplate(fillTemplate(t, event.EventType(), map[string]string{loc.path: member}, keep)) == nil
				}
			}
		}
	}
	for id, ok := range works {
		if !ok {
			t.Errorf("choice %s decodes in no template", id)
		}
	}
	if len(works) < 60 {
		t.Fatalf("only %d choice members were exercised", len(works))
	}
}

type templateChoice struct {
	path, set string
	members   []string
}

func templateChoiceLocations(t *testing.T, raw []byte, notes []templateNote) []templateChoice {
	var out []templateChoice
	for _, n := range notes {
		if n.Kind == "choose" && n.Union.tag == "" {
			out = append(out, templateChoice{templatePath(n.Path) + ".(one of)", strings.Join(n.Union.members, "|"), n.Union.members})
		}
	}
	var tree []any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch node := v.(type) {
		case map[string]any:
			for key, value := range node {
				walk(value, strings.TrimPrefix(path+"."+key, "."))
			}
		case []any:
			for _, item := range node {
				walk(item, path+"[0]")
			}
		case string:
			if kind, hint, ok := templateKind(node); ok && kind == "one of" {
				out = append(out, templateChoice{path, hint, strings.Split(hint, " | ")})
			}
		}
	}
	walk(tree[0].(map[string]any)["data"], "")
	return out
}

func TestTemplateMintsFreshIDsOnlyWhereTheEventCreates(t *testing.T) {
	for eventType, paths := range templateMints {
		first, _ := fillTemplateRaw(t, eventType)
		second, _ := fillTemplateRaw(t, eventType)
		for _, path := range paths {
			a, b := first[path], second[path]
			if !model.ValidID(model.ID(a)) || a == b {
				t.Errorf("%s %s: minted %q then %q; want two fresh valid ids", eventType, path, a, b)
			}
		}
	}
	// A reference is never minted: an attempt.terminal names an existing attempt.
	ids, _ := fillTemplateRaw(t, "attempt.terminal")
	if model.ValidID(model.ID(ids["attempt_id"])) {
		t.Fatal("attempt.terminal minted the attempt it must reference")
	}
}

func fillTemplateRaw(t *testing.T, eventType model.EventType) (map[string]string, []byte) {
	raw, _, err := buildTemplate(eventType)
	if err != nil {
		t.Fatal(err)
	}
	var tree []map[string]any
	json.Unmarshal(raw, &tree)
	out := map[string]string{}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch node := v.(type) {
		case map[string]any:
			for key, value := range node {
				walk(value, strings.TrimPrefix(path+"."+key, "."))
			}
		case []any:
			for _, item := range node {
				walk(item, path+"[0]")
			}
		case string:
			out[path] = node
		}
	}
	walk(tree[0]["data"], "")
	return out, raw
}

// The model's own source is the authority for enum members and the event set.
func TestTemplateTablesCoverTheModel(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob(filepath.Join("..", "..", "internal", "model", "*.go"))
	known := map[string]bool{}
	norm := func(members []string) string {
		sorted := append([]string(nil), members...)
		sort.Strings(sorted)
		return strings.Join(sorted, "|")
	}
	for _, members := range templateEnumTypes {
		known[norm(members)] = true
	}
	for _, members := range templateEnumFields {
		known[norm(members)] = true
	}
	for _, u := range templateUnions {
		known[norm(u.members)] = true
	}
	known[norm(templateAvailability.members)] = true
	declared := map[string]bool{}
	sets := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "oneOf" && len(call.Args) > 2 {
					var members []string
					for _, arg := range call.Args[2:] {
						if lit, ok := arg.(*ast.BasicLit); ok {
							value, _ := strconv.Unquote(lit.Value)
							members = append(members, value)
						}
					}
					sets++
					if !known[norm(members)] {
						t.Errorf("%s: the model's enum %v is not in the template tables", fset.Position(call.Pos()), members)
					}
				}
			}
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "EventType" && fn.Body != nil && len(fn.Body.List) == 1 {
				if ret, ok := fn.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
					if lit, ok := ret.Results[0].(*ast.BasicLit); ok {
						value, _ := strconv.Unquote(lit.Value)
						declared[value] = true
					}
				}
			}
			return true
		})
	}
	listed := map[string]bool{}
	for _, event := range templateEvents {
		listed[string(event.EventType())] = true
	}
	if sets < 15 || len(declared) != 25 || fmt.Sprint(declared) != fmt.Sprint(listed) {
		t.Fatalf("model declares %d event types %v; template lists %v (%d enum sets read)", len(declared), declared, listed, sets)
	}
}

func TestTemplateFreshProcessWritesNothing(t *testing.T) {
	root, _ := cliFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, []byte, error) {
		command := exec.Command(binary, append([]string{"-test.run=^TestDatumMainProcess$", "--"}, args...)...)
		command.Dir = root
		command.Env = append(os.Environ(), "DATUM_MAIN_TEST_PROCESS=1")
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	before := cliTree(t, root)
	out, notes, err := run("template", "proof.admit")
	if err != nil || !json.Valid(out) || !strings.Contains(string(notes), "choose") {
		t.Fatalf("template: %v\n%s\n%s", err, out, notes)
	}
	if _, err := model.DecodeEvents(out); err != nil {
		t.Fatalf("the template is not a capture-shaped event array: %v", err)
	}
	if help, _, err := run("template", "--help"); err != nil || !strings.Contains(string(help), "artifact.dispose") {
		t.Fatalf("usage must list the event types: %v\n%s", err, help)
	}
	if _, _, err := run("template", "no.such"); err == nil {
		t.Fatal("an unknown event type must fail")
	}
	if after := cliTree(t, root); after != before {
		t.Fatalf("template wrote into the project:\n%s\n%s", before, after)
	}
}

// cliTree fingerprints the project root and the Datum home (store.Home).
func cliTree(t *testing.T, root string) string {
	t.Helper()
	home, err := store.Home()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, dir := range []string{root, home} {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || cacheImagePath(path) || info.IsDir() && path == filepath.Join(root, ".datum", "cache") {
				return nil // the disposable snapshot cache may be refreshed by any read
			}
			data := []byte{}
			if !info.IsDir() {
				data, _ = os.ReadFile(path)
			}
			lines = append(lines, fmt.Sprintf("%s %v %s", path, info.Mode(), model.HashBytes(data)))
			return nil
		})
	}
	return strings.Join(lines, "\n")
}

// A key the model decodes when absent but admission requires reads
// "required", never "optional"; the table entry must name a real omitempty
// field, or it is a note about nothing.
func TestTemplateRequiredKeysAreNotOptional(t *testing.T) {
	for field, why := range templateRequired {
		f, ok := field.owner.FieldByName(field.field)
		if !ok || !strings.Contains(f.Tag.Get("json"), ",omitempty") || why == "" {
			t.Errorf("%s.%s: a required entry must name an omitempty field and say why", field.owner, field.field)
		}
	}
	_, notes, err := buildTemplate("proof.admit")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, n := range notes {
		kinds[templatePath(n.Path)] = n.Kind
	}
	if kinds["verdict"] != "required" || kinds["evidence[0].code_change"] != "optional" {
		t.Fatalf("verdict must read required and a truly optional key optional: %v", kinds)
	}
	if text := renderTemplateNotes("proof.admit", notes); !strings.Contains(text, "required verdict: required at admission") || strings.Contains(text, "optional verdict") {
		t.Fatalf("the notes misstate verdict:\n%s", text)
	}
}

// A narrowed union keeps only the members the model accepts at that field:
// the task's accepter is a known id, and the model refuses an unknown one.
func TestTemplateAccepterIsAKnownIDOnly(t *testing.T) {
	if len(templateFieldUnions) != 1 {
		t.Fatalf("a new narrowed union needs its refusal checked here: %v", templateFieldUnions)
	}
	raw, notes, err := buildTemplate("task.create")
	if err != nil {
		t.Fatal(err)
	}
	var skeleton []map[string]any
	if err := json.Unmarshal(raw, &skeleton); err != nil {
		t.Fatal(err)
	}
	if accepter := skeleton[0]["data"].(map[string]any)["spec"].(map[string]any)["accepter"].(map[string]any); len(accepter) != 1 || accepter["id"] == nil {
		t.Fatalf("the accepter skeleton must hold only id: %v", accepter)
	}
	if text := renderTemplateNotes("task.create", notes); !strings.Contains(text, "only     spec.accepter: id only") || strings.Contains(text, "spec.accepter: id | unknown_reason") {
		t.Fatalf("the notes misstate the accepter:\n%s", text)
	}
	for _, tc := range []struct {
		accepter map[string]any
		decodes  bool
	}{{map[string]any{"id": "someone"}, true}, {map[string]any{"unknown_reason": "filled"}, false}} {
		var tree []map[string]any
		if err := json.Unmarshal(fillTemplate(t, "task.create", nil, map[string]bool{"spec.accepter": true}), &tree); err != nil {
			t.Fatal(err)
		}
		tree[0]["data"].(map[string]any)["spec"].(map[string]any)["accepter"] = tc.accepter
		data, _ := json.Marshal(tree)
		if err := decodeTemplate(data); (err == nil) != tc.decodes {
			t.Errorf("accepter %v: decodes=%v, want %v (%v)", tc.accepter, err == nil, tc.decodes, err)
		}
	}
}
