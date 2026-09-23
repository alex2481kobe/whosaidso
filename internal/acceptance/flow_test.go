package acceptance_test

// U14 independent end-to-end flows. Every step runs the built `datum` binary
// in a fresh process, inside a temp project with its own datum.toml and its
// own HOME (so its own intake), and every fact is read back through the read
// surface. Every read is taken three times, as the default (brief) text, as
// --full and as --json. --full is compared with --json field by field; every
// value the brief prints must equal the JSON leaf at its recorded path or be a
// marked prefix of it, and every count must equal that array's length. Every
// answer must carry the watermark of the ledger it was read from. Refusals must
// leave .datum/events byte-identical.
//
// Event payloads are built with the model types only to produce the JSON a
// lane would pipe to `datum capture`; nothing here calls the write, store or
// reduce packages directly.
//
// Supersede and artifact.dispose are left to a later lane on purpose.
//
// Changed when the reducer types gained json tags: every JSON path here is snake_case.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/query"
)

type flowWorld struct {
	t          *testing.T
	root, home string
	project    model.ProjectID
	n          int
	scope      model.Scope
	claim      model.RecordRef
	instrument model.RecordRef
	attempt    model.ID
	criterion  model.CriterionRef
}

const flowLane = "lane"

func flowNew(t *testing.T) *flowWorld {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("flows use POSIX producers; Windows is out of scope")
	}
	w := &flowWorld{t: t, root: t.TempDir(), home: t.TempDir(), project: "flow/review", n: 100}
	w.put("datum.toml", []byte("id = \"flow/review\"\nledger = \".datum/events\"\n"))
	w.scope = model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this flow fixture", Limitations: "a temp project"}
	return w
}

func (w *flowWorld) id() model.ID { w.n++; return recID(w.n) }

func (w *flowWorld) put(rel string, body []byte) { pvPut(w.t, w.root, rel, body) }

func (w *flowWorld) ref(id model.ID, revision model.Revision) model.RecordRef {
	return model.RecordRef{Project: w.project, RecordID: id, Revision: revision}
}

// cli runs the built binary in a fresh process at the project root, with no
// DATUM_ACTOR inherited, so every attribution is the one the step names.
func (w *flowWorld) cli(stdin []byte, args ...string) ([]byte, error) {
	w.t.Helper()
	cmd := exec.Command(pvDatum(w.t), args...)
	cmd.Dir, cmd.Stdin = w.root, bytes.NewReader(stdin)
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DATUM_ACTOR=") && !strings.HasPrefix(entry, "HOME=") {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, "HOME="+w.home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("datum %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (w *flowWorld) capture(author string, events ...model.TypedEvent) model.ID {
	w.t.Helper()
	raw := make([]model.Event, len(events))
	for i, e := range events {
		raw[i] = recEncode(w.t, e)
	}
	body, err := model.Encode(raw)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := w.cli(body, "capture", "--actor", author, "--command-id", string(w.id()), "--events", "-")
	if err != nil {
		w.t.Fatalf("control: capture must durably write intake: %v", err)
	}
	return flowPacket(w.t, out)
}

func flowPacket(t *testing.T, out []byte) model.ID {
	t.Helper()
	var ref model.PacketRef
	if err := json.Unmarshal(out, &ref); err != nil || ref.CommandID == "" {
		t.Fatalf("expected a packet reference, got %q: %v", out, err)
	}
	return ref.CommandID
}

func (w *flowWorld) review(outcome string, packets ...model.ID) error {
	w.t.Helper()
	args := []string{"admit", "--command-id", string(w.id()), "--actor", "coordinator", "--outcome", outcome, "--reason", "U14 flow review"}
	for _, p := range packets {
		args = append(args, string(p))
	}
	_, err := w.cli(nil, args...)
	return err
}

func (w *flowWorld) mustAdmit(author string, events ...model.TypedEvent) {
	w.t.Helper()
	if err := w.review("accepted", w.capture(author, events...)); err != nil {
		w.t.Fatalf("control: fixture step must admit: %v", err)
	}
}

// ledger is every byte under .datum/events, by file name.
func (w *flowWorld) ledger() map[string][]byte {
	w.t.Helper()
	out := map[string][]byte{}
	dir := filepath.Join(w.root, ".datum", "events")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		w.t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			w.t.Fatal(err)
		}
		out[e.Name()] = data
	}
	return out
}

// refused asserts that step fails and that the ledger is byte-identical after.
func (w *flowWorld) refused(what string, step func() error) {
	w.t.Helper()
	before := w.ledger()
	err := step()
	if err == nil {
		w.t.Errorf("%s: expected a refusal, but it was admitted", what)
	} else {
		w.t.Logf("%s: refused: %v", what, err)
	}
	if after := w.ledger(); !reflect.DeepEqual(before, after) {
		w.t.Errorf("%s: a refused admission changed .datum/events (%d files before, %d after)", what, len(before), len(after))
	}
}

// ---- reads: both surfaces, one set of facts, one watermark -----------------

var flowBundleName = regexp.MustCompile(`^([0-9]{8})-([0-9A-Z]{26})\.json$`)

// read runs the command as brief text, as --full and as --json. --full must
// carry the same facts as --json; the brief must agree with --json under the
// brief's rule (flowBriefAgrees). It requires the watermark to name the
// ledger's actual head, and returns the decoded JSON answer.
// Changed with the brief becoming the default text: the lossless comparison
// moved to --full and the default text is checked by the brief's rule.
func (w *flowWorld) read(args ...string) map[string]any {
	w.t.Helper()
	exported := w.readFlag("--json", args...)
	answer := flowDecode(w.t, exported)
	full := w.readFlag("--full", args...)
	fromText, err := flowOutline(string(full))
	if err != nil {
		w.t.Fatalf("datum %s --full: text answer is not a readable outline: %v\n%s", strings.Join(args, " "), err, full)
	}
	fromJSON := map[string]string{}
	flowFlatten("answer", answer, fromJSON)
	if !reflect.DeepEqual(fromText, fromJSON) {
		w.t.Errorf("datum %s: --full text and --json disagree: %s", strings.Join(args, " "), flowDiff(fromText, fromJSON))
	}
	text, err := w.cli(nil, args...)
	if err != nil {
		w.t.Fatalf("text read: %v", err)
	}
	flowBriefAgrees(w.t, "datum "+strings.Join(args, " "), exported, answer, string(text))
	w.watermark(args, answer)
	return answer
}

// flowBriefAgrees checks brief text against the JSON export it claims to
// read: the text is exactly the brief of that export, it prints no absent
// path, every fact it records is printed, in order, and equals the JSON leaf
// at its path (a string cut short is a strict prefix marked with …), and every
// count is the length of the array at its path.
func flowBriefAgrees(t testing.TB, what string, exported []byte, answer map[string]any, text string) {
	t.Helper()
	want, facts, err := query.BriefOf(exported)
	if err != nil {
		t.Fatalf("%s: the brief of the JSON export fails: %v", what, err)
	}
	if text != want {
		t.Errorf("%s: the text is not the brief of the same answer's --json:\n%s\nwant\n%s", what, text, want)
		return
	}
	if len(facts) == 0 || strings.Contains(text, "UNKNOWN(absent)") {
		t.Errorf("%s: the brief shows no facts, or a path the JSON lacks:\n%s", what, text)
		return
	}
	rest := text
	for _, f := range facts {
		where := strings.Join(f.Path, "/")
		v, ok := flowAt(answer, f.Path)
		if !ok {
			t.Errorf("%s: the brief shows %s from %s, which the JSON does not have", what, f.Display, where)
			continue
		}
		at := strings.Index(rest, f.Display)
		if at < 0 {
			t.Errorf("%s: the brief recorded %q from %s but did not print it there", what, f.Display, where)
			continue
		}
		rest = rest[at+len(f.Display):]
		if f.Count {
			if xs, isList := v.([]any); !isList || strconv.Itoa(len(xs)) != f.Display {
				t.Errorf("%s: the brief counts %s at %s, the JSON has %v", what, f.Display, where, v)
			}
			continue
		}
		leaf, _ := json.Marshal(v)
		s, isString := v.(string)
		shown := strings.TrimSuffix(f.Display, "…")
		if isString && strings.HasPrefix(shown, `"`) {
			if err := json.Unmarshal([]byte(shown), &shown); err != nil {
				t.Errorf("%s: the brief quoted %q at %s undecodably", what, f.Display, where)
				continue
			}
		}
		switch {
		case f.Prefix && (!isString || !strings.HasSuffix(f.Display, "…") || !strings.HasPrefix(s, shown) || s == shown):
			t.Errorf("%s: the brief shows %q at %s as a cut of %s, but it is not a marked strict prefix", what, f.Display, where, leaf)
		case !f.Prefix && isString && (f.Display != shown && !strings.HasPrefix(f.Display, `"`) || shown != s):
			t.Errorf("%s: the brief shows %q at %s, the JSON has %s", what, f.Display, where, leaf)
		case !f.Prefix && !isString && f.Display != string(leaf):
			t.Errorf("%s: the brief shows %q at %s, the JSON has %s", what, f.Display, where, leaf)
		}
	}
}

// flowAt walks decoded JSON by a brief path: JSON-quoted keys and [i] indexes.
func flowAt(root any, path []string) (any, bool) {
	v := root
	for _, step := range path {
		if strings.HasPrefix(step, "[") {
			i, err := strconv.Atoi(strings.Trim(step, "[]"))
			xs, ok := v.([]any)
			if err != nil || !ok || i < 0 || i >= len(xs) {
				return nil, false
			}
			v = xs[i]
			continue
		}
		var key string
		m, ok := v.(map[string]any)
		if json.Unmarshal([]byte(step), &key) != nil || !ok {
			return nil, false
		}
		if v, ok = m[key]; !ok {
			return nil, false
		}
	}
	return v, true
}

func (w *flowWorld) readJSON(args ...string) map[string]any {
	w.t.Helper()
	return flowDecode(w.t, w.readFlag("--json", args...))
}

// readFlag runs a read with one rendering flag and returns its stdout.
func (w *flowWorld) readFlag(flag string, args ...string) []byte {
	w.t.Helper()
	// Flags follow the (sub)command and precede the optional record ID.
	at := 1
	if args[0] == "task" || args[0] == "intake" {
		at = 2
	}
	withFlag := append(append(append([]string{}, args[:at]...), flag), args[at:]...)
	out, err := w.cli(nil, withFlag...)
	if err != nil {
		w.t.Fatalf("%s read: %v", flag, err)
	}
	return out
}

// openTasks reads `datum show` on both surfaces (same facts, same watermark,
// via read) and keeps the TASK records whose status is not CLOSED, IN FLIGHT
// included. That is exactly what `datum task todo` selected before the owner
// removed the verb as redundant (R12); the todo preset's sections differ.
func (w *flowWorld) openTasks() []any {
	w.t.Helper()
	var open []any
	for _, rec := range flowList(w.read("show"), "records") {
		if flowStr(rec, "fact", "kind") == string(model.Task) && flowGet(rec, "task") != nil && flowStr(rec, "task", "status") != "CLOSED" {
			open = append(open, rec)
		}
	}
	return open
}

func flowDecode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var v map[string]any
	if err := decoder.Decode(&v); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, data)
	}
	return v
}

func (w *flowWorld) watermark(args []string, answer map[string]any) {
	w.t.Helper()
	var seq, head string
	for name := range w.ledger() {
		if m := flowBundleName.FindStringSubmatch(name); m != nil && m[1] > seq {
			seq, head = m[1], m[2]
		}
	}
	mark, _ := answer["watermark"].(map[string]any)
	if mark == nil {
		w.t.Errorf("datum %s: the answer carries no watermark", strings.Join(args, " "))
		return
	}
	if seq == "" {
		if flowStr(mark, "head", "state") != "UNKNOWN" {
			w.t.Errorf("datum %s: an empty ledger's watermark names a head: %v", strings.Join(args, " "), mark)
		}
		return
	}
	want, _ := strconv.ParseUint(seq, 10, 64)
	if flowStr(mark, "sequence") != strconv.FormatUint(want, 10) || flowStr(mark, "head", "command_id") != head {
		w.t.Errorf("datum %s: watermark %v does not name the ledger head %s-%s", strings.Join(args, " "), mark, seq, head)
	}
}

// flowOutline parses the text surface back into path -> value, independently
// of the renderer: two-space indentation, a label, then a JSON literal or nothing.
func flowOutline(text string) (map[string]string, error) {
	out := map[string]string{}
	var stack []string
	for n, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if indent%2 != 0 || indent/2 > len(stack) {
			return nil, fmt.Errorf("line %d: bad indentation", n+1)
		}
		stack = stack[:indent/2]
		label, rest, err := flowLabel(trimmed)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
		if rest == "" {
			stack = append(stack, label)
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(rest))
		decoder.UseNumber()
		var v any
		if err := decoder.Decode(&v); err != nil {
			return nil, fmt.Errorf("line %d: value %q is not a JSON literal", n+1, rest)
		}
		flowFlatten(strings.Join(append(append([]string{}, stack...), label), "\x00"), v, out)
	}
	return out, nil
}

func flowLabel(s string) (label, rest string, err error) {
	end := strings.Index(s, ":")
	if strings.HasPrefix(s, `"`) {
		end = -1
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				end = i + 1
				break
			}
		}
		if end < 0 || end >= len(s) || s[end] != ':' {
			return "", "", fmt.Errorf("unterminated key in %q", s)
		}
	}
	if end < 0 {
		return "", "", fmt.Errorf("no label in %q", s)
	}
	return s[:end], strings.TrimPrefix(s[end+1:], " "), nil
}

func flowFlatten(path string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			out[path] = "{}"
		}
		for k, val := range x {
			key, _ := json.Marshal(k)
			flowFlatten(path+"\x00"+string(key), val, out)
		}
	case []any:
		if len(x) == 0 {
			out[path] = "[]"
		}
		for i, val := range x {
			flowFlatten(fmt.Sprintf("%s\x00[%d]", path, i), val, out)
		}
	default:
		b, _ := json.Marshal(x)
		out[path] = string(b)
	}
}

func flowDiff(a, b map[string]string) string {
	var diffs []string
	for k, v := range a {
		if b[k] != v {
			diffs = append(diffs, fmt.Sprintf("%q text=%s json=%s", strings.ReplaceAll(k, "\x00", "/"), v, b[k]))
		}
	}
	for k, v := range b {
		if _, ok := a[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%q missing from text, json=%s", strings.ReplaceAll(k, "\x00", "/"), v))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 5 {
		diffs = append(diffs[:5], fmt.Sprintf("... %d more", len(diffs)-5))
	}
	return strings.Join(diffs, "; ")
}

// flowGet walks decoded JSON by map key or slice index.
func flowGet(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			s, _ := v.([]any)
			if k < 0 || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

func flowStr(v any, path ...any) string {
	switch x := flowGet(v, path...).(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func flowList(v any, path ...any) []any {
	s, _ := flowGet(v, path...).([]any)
	return s
}

// record is the single record `datum show ID` selects.
func (w *flowWorld) record(id model.ID) map[string]any {
	w.t.Helper()
	answer := w.read("show", string(id))
	records := flowList(answer, "records")
	if flowStr(answer, "result") != "KNOWN" || len(records) != 1 {
		w.t.Fatalf("show %s: expected one KNOWN record, got %s with %d records", id, flowStr(answer, "result"), len(records))
	}
	r, _ := records[0].(map[string]any)
	return r
}

// ---- fixtures ----------------------------------------------------------------

type flowTask struct {
	ref     model.RecordRef
	spec    model.TaskSpec
	attempt model.ID
}

// task admits a task and an attempt held by the lane.
func (w *flowWorld) task(intent string) flowTask {
	w.t.Helper()
	prov := model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}}
	spec := model.TaskSpec{Intent: intent, Subject: "the flow fixture", Scope: w.scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: w.id(), Revision: 1, Criterion: "the fixture measurement is delivered"}},
		ContextRefs:        []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: flowLane}}
	create := &model.TaskCreate{ID: w.id(), Provenance: prov, Spec: spec}
	w.mustAdmit(flowLane, create)
	task := flowTask{ref: w.ref(create.ID, 1), spec: spec, attempt: w.id()}
	w.mustAdmit(flowLane, &model.TaskStart{Task: task.ref, Actor: model.Actor{ID: flowLane}, AttemptID: task.attempt})
	return task
}

// handback captures a receipt through the CLI and returns its packet.
func (w *flowWorld) handback(attempt model.ID, outcome string, extra ...string) (model.ID, error) {
	w.t.Helper()
	args := append([]string{"handback", "--command-id", string(w.id()), "--actor", flowLane, "--attempt-id", string(attempt),
		"--outcome", outcome, "--reason", "the flow ends this attempt " + outcome, "--next-action", "next: " + outcome}, extra...)
	out, err := w.cli(nil, args...)
	if err != nil {
		return "", err
	}
	return flowPacket(w.t, out), nil
}

// proofWorld admits a task, attempt, claim and KNOWN-validated instrument, and
// fixes a criterion whose pinned example FAILS, so PROVEN is only reachable by
// reading bytes a real run wrote.
func flowProofWorld(t *testing.T) *flowWorld {
	t.Helper()
	w := flowNew(t)
	task := w.task("measure the pose sweep")
	w.attempt = task.attempt
	impl, validation := []byte(`{"tool":"measure"}`), []byte(`{"validated":"against a known pose sweep"}`)
	w.put("tools/measure.json", impl)
	w.put("validation/measure.json", validation)
	prov := model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}}
	claim := &model.ClaimAssert{ID: w.id(), Provenance: prov, Spec: model.ClaimSpec{Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: w.scope, ExternalRefs: []model.ExternalReference{}}}
	instrument := &model.InstrumentDeclare{ID: w.id(), Provenance: prov, Spec: model.InstrumentSpec{QuestionAnswered: "pose penetration depth", BlindTo: "unmeasured poses",
		NotAnswered: "production behaviour", ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "the fixture sweep",
		ImplementationRef: pvPin(impl, "tools/measure.json"),
		Validation:        recKnown(model.InstrumentValidation{Ref: pvPin(validation, "validation/measure.json"), Version: "v1"})}}
	w.mustAdmit(flowLane, claim, instrument)
	w.claim, w.instrument = w.ref(claim.ID, 1), w.ref(instrument.ID, 1)
	w.put("out/result.json", []byte(pvFail))
	w.fix(w.id(), 1)
	return w
}

func (w *flowWorld) criterionFix(id model.ID, revision model.Revision) *model.CriterionFix {
	result, population := pvPin([]byte(pvFail), "out/result.json"), pvPin([]byte(pvFail), "out/result.json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	return &model.CriterionFix{Claim: w.claim, CriterionID: id, Revision: revision, Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
}

func (w *flowWorld) fix(id model.ID, revision model.Revision) {
	w.t.Helper()
	w.mustAdmit(flowLane, w.criterionFix(id, revision))
	w.criterion = model.CriterionRef{Claim: w.claim, CriterionID: id, Revision: revision}
	time.Sleep(2 * time.Millisecond) // the criterion's bundle strictly precedes any launch
}

// run is a real `datum run` of a producer script under the current criterion.
func (w *flowWorld) run(script string) (model.ID, []model.ID, error) {
	w.t.Helper()
	w.put("tools/run.sh", []byte(script))
	out, err := w.cli(nil, "run", "--actor", flowLane, "--attempt-id", string(w.attempt), "--instrument", string(w.instrument.RecordID),
		"--claim", string(w.claim.RecordID), "--claim-revision", "1", "--criterion-id", string(w.criterion.CriterionID),
		"--criterion-revision", strconv.FormatUint(uint64(w.criterion.Revision), 10), "--", "/bin/sh", "tools/run.sh")
	var result struct {
		// datum run prints snake_case keys (coordinator change, in the open).
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if len(out) > 0 {
		if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
			w.t.Fatalf("run printed unparseable output %q: %v", out, jsonErr)
		}
	}
	packets := []model.ID{}
	for _, p := range []model.PacketRef{result.StartPacket, result.SealPacket} {
		if p.CommandID != "" {
			packets = append(packets, p.CommandID)
		}
	}
	return result.Envelope.InvocationID, packets, err
}

// admittedRun runs a producer and admits both packets.
func (w *flowWorld) admittedRun(body string) model.ID {
	w.t.Helper()
	id, packets, err := w.run(pvProducer(body))
	if err != nil || len(packets) != 2 {
		w.t.Fatalf("control: datum run must seal and print both packets: %v, %v", packets, err)
	}
	if err := w.review("accepted", packets...); err != nil {
		w.t.Fatalf("control: the run's start and seal admit: %v", err)
	}
	return id
}

func (w *flowWorld) proofEvent(members map[model.ID]string) *model.ProofAdmit {
	evidence := []model.ObservationDisposition{}
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		evidence = append(evidence, model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: w.project, InvocationID: model.ID(id)},
			Disposition: members[model.ID(id)], Reason: "dispositioned by the lane's judgment"})
	}
	return &model.ProofAdmit{Claim: w.claim, CriterionRef: w.criterion, Evidence: evidence,
		Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: flowLane}, Reason: "the complete family satisfies the frozen criterion"}}
}

func (w *flowWorld) prove(members map[model.ID]string) error {
	w.t.Helper()
	return w.review("accepted", w.capture(flowLane, w.proofEvent(members)))
}

func (w *flowWorld) claimStatus() string {
	w.t.Helper()
	return flowStr(w.record(w.claim.RecordID), "claim", "status")
}

// handEnvelope is a lane's hand-captured start envelope under the current
// criterion; seal completes it with one output in the run's own directory.
func (w *flowWorld) handEnvelope(id model.ID, started time.Time) model.InvocationEnvelope {
	unknownMap := recUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	return model.InvocationEnvelope{InvocationID: id, AttemptID: w.attempt, InstrumentRef: w.instrument, CriterionRef: recKnown(w.criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: w.project, SourceRefs: []model.ArtifactRef{}, MachineID: recUnknown[model.ID]("fixture"), Head: recUnknown[model.GitHead]("fixture"), Dirty: recUnknown[bool]("fixture")},
		Argv:                    []string{"/bin/sh", "tools/run.sh"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknownMap, ConditionsObserved: unknownMap, Isolation: recUnknown[model.Isolation]("not enforced"),
		StartedAt: started.UTC(), ObservedAt: recUnknown[time.Time]("not launched"), Outcome: recUnknown[model.ProcessOutcome]("not launched"),
		OutputRefs: recUnknown[[]model.ArtifactRef]("not launched"), Visual: recUnknown[model.VisualObservation]("numeric")}
}

func (w *flowWorld) handRun(started time.Time, body string) (model.ID, []model.TypedEvent) {
	id := w.id()
	env := w.handEnvelope(id, started)
	rel := pvRunPath(id, "out/result.json")
	w.put(rel, []byte(body))
	exit := 0
	seal := env
	seal.ObservedAt = recKnown(started.UTC().Add(time.Millisecond))
	seal.Outcome = recKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	seal.OutputRefs = recKnown([]model.ArtifactRef{pvPin([]byte(body), rel)})
	seal.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{})
	seal.ConditionsObserved = recKnown(map[string]model.Availability[model.Scalar]{})
	return id, []model.TypedEvent{&model.InvocationStart{Envelope: env}, &model.InvocationSeal{StartRef: model.InvocationRef{Project: w.project, InvocationID: id}, Envelope: seal}}
}

// ---- 1. honest stopping --------------------------------------------------------

func TestFlowHonestStopping(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	type want struct {
		status, reason, reasonActor string
		extra                       []string
	}
	cases := []struct {
		outcome string
		want    want
	}{
		{"stopped", want{status: "READY"}},
		{"refused", want{status: "READY"}},
		{"no-reading", want{status: "READY"}},
		{"measurement-impossible", want{status: "READY"}},
		{"runner-died", want{status: "READY"}},
		{"harness-broken", want{status: "READY"}},
		{"out-of-scope", want{status: "BLOCKED", reason: "resume", reasonActor: "reassignee",
			extra: []string{"--hold-reason", "resume", "--hold-actor", "reassignee", "--hold-criterion", "the owner reassigns the renderer work to the reassignee"}}},
		{"blocked-mid-task", want{status: "BLOCKED", reason: "prerequisite", reasonActor: "owner",
			extra: []string{"--hold-reason", "prerequisite", "--hold-actor", "owner", "--hold-criterion", "the owner restores the fixture machine"}}},
	}
	for _, c := range cases {
		task := w.task("stop honestly: " + c.outcome)
		if got := flowStr(w.record(task.ref.RecordID), "task", "status"); got != "IN FLIGHT" {
			t.Fatalf("control: a started task is IN FLIGHT, got %s", got)
		}
		extra := c.want.extra
		if extra != nil {
			// The receipt alone is refused: the hold must be in the same bundle.
			bare, err := w.handback(task.attempt, c.outcome)
			if err != nil {
				t.Fatalf("control: the receipt itself captures: %v", err)
			}
			w.refused(c.outcome+" without its bundled hold", func() error { return w.review("accepted", bare) })
			if err := w.review("rejected", bare); err != nil {
				t.Fatalf("control: the holdless receipt can be turned away: %v", err)
			}
			if c.outcome == "out-of-scope" {
				// A hold that is not a resume hold proposes no reassignment.
				wrong, err := w.handback(task.attempt, c.outcome, "--hold-id", string(w.id()), "--hold-reason", "prerequisite",
					"--hold-actor", "reassignee", "--hold-criterion", "the owner reassigns the renderer work")
				if err != nil {
					t.Fatalf("control: the receipt itself captures: %v", err)
				}
				w.refused("out-of-scope whose hold is not a resume/reassignment hold", func() error { return w.review("accepted", wrong) })
				if err := w.review("rejected", wrong); err != nil {
					t.Fatal(err)
				}
			}
			extra = append([]string{"--hold-id", string(w.id())}, extra...)
		}
		packet, err := w.handback(task.attempt, c.outcome, extra...)
		if err != nil {
			t.Fatalf("%s: handback must capture: %v", c.outcome, err)
		}
		if err := w.review("accepted", packet); err != nil {
			t.Fatalf("%s: an honest non-success receipt must admit: %v", c.outcome, err)
		}
		r := w.record(task.ref.RecordID)
		if got := flowStr(r, "task", "status"); got != c.want.status {
			t.Errorf("%s: expected task %s, got %s", c.outcome, c.want.status, got)
		}
		if got := flowStr(r, "task", "revision"); got != "1" {
			t.Errorf("%s: the receipt changed the task's revision to %s; its scope is not the lane's to amend", c.outcome, got)
		}
		if got := flowStr(r, "task", "outcome"); got != "UNKNOWN" {
			t.Errorf("%s: an open task reports closure outcome %s", c.outcome, got)
		}
		terminal := flowGet(r, "task", "attempts", 0, "terminal")
		if flowStr(terminal, "outcome") != c.outcome || flowStr(terminal, "next_action") != "next: "+c.outcome {
			t.Errorf("%s: the receipt is not readable as authored: %v", c.outcome, terminal)
		}
		if c.want.reason != "" {
			found := false
			for _, reason := range flowList(r, "task", "reasons") {
				if flowStr(reason, "kind") == c.want.reason && flowStr(reason, "actor", "id") == c.want.reasonActor {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: expected a %s reason waiting on %s, got %v", c.outcome, c.want.reason, c.want.reasonActor, flowGet(r, "task", "reasons"))
			}
		}
		// The next action is readable from the continuation brief.
		brief := w.readJSON("continue", string(task.ref.RecordID))
		attempt := flowGet(brief, "preset", "continue", "attempts", 0)
		if flowStr(attempt, "outcome") != c.outcome || flowStr(attempt, "next_action") != "next: "+c.outcome || flowStr(attempt, "live") != "false" {
			t.Errorf("%s: continue does not carry the receipt's outcome and next action: %v", c.outcome, attempt)
		}
		// Open work stays on the todo surfaces.
		open := false
		for _, rec := range w.openTasks() {
			open = open || flowStr(rec, "fact", "key", "id") == string(task.ref.RecordID)
		}
		if !open {
			t.Errorf("%s: the task left the open (not CLOSED) tasks in `show`; a non-success receipt must leave it open", c.outcome)
		}
		section := map[string]string{"READY": "ready", "BLOCKED": "blocked"}[c.want.status]
		listed := false
		for _, rec := range flowList(w.read("todo"), "preset", section) {
			listed = listed || flowStr(rec, "fact", "key", "id") == string(task.ref.RecordID)
		}
		if !listed {
			t.Errorf("%s: the task is not in the todo %s section", c.outcome, section)
		}
	}

	// runner-died with the observer lost: reconciliation is owed, so BLOCKED
	// wins over READY.
	task := w.task("runner died, observer lost")
	packet, err := w.handback(task.attempt, "runner-died", "--reconciliation-owed")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.review("accepted", packet); err != nil {
		t.Fatalf("control: runner-died with reconciliation owed admits: %v", err)
	}
	r := w.record(task.ref.RecordID)
	if flowStr(r, "task", "status") != "BLOCKED" || flowStr(r, "task", "reasons", 0, "kind") != "reconciliation" {
		t.Errorf("runner-died with reconciliation owed must be BLOCKED on reconciliation, got %s %v", flowStr(r, "task", "status"), flowGet(r, "task", "reasons"))
	}
}

// DATUM-CONTRACT (out-of-scope): "Preserve the owed work; propose
// reassignment." The command's own usage says out-of-scope "needs a resume
// hold with an authored reassignment criterion and actor". The gate checks
// only the hold's reason, so a resume hold that names nobody is admitted and
// the task waits on an unknown actor: the work is preserved, but no
// reassignment was proposed.
func TestFlowOutOfScopeReassignmentNamesAnActor(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	control := w.task("control: out of scope, reassigned")
	packet, err := w.handback(control.attempt, "out-of-scope", "--hold-id", string(w.id()), "--hold-reason", "resume",
		"--hold-actor", "reassignee", "--hold-criterion", "the owner reassigns the renderer work")
	if err != nil || w.review("accepted", packet) != nil {
		t.Fatalf("control: out-of-scope with a named reassignment admits: %v", err)
	}
	task := w.task("out of scope, reassigned to nobody")
	packet, err = w.handback(task.attempt, "out-of-scope", "--hold-id", string(w.id()), "--hold-reason", "resume",
		"--hold-criterion", "the owner reassigns the renderer work")
	if err != nil {
		t.Fatalf("control: the receipt captures: %v", err)
	}
	w.refused("out-of-scope whose resume hold names no reassignment actor", func() error { return w.review("accepted", packet) })
	if t.Failed() {
		r := w.record(task.ref.RecordID)
		t.Logf("the admitted task now reads %s waiting on %v; expected the receipt to be refused because the reassignment it must propose names no actor",
			flowStr(r, "task", "status"), flowGet(r, "task", "waiting_actors"))
	}
}

// ---- 2. proof -----------------------------------------------------------------

func TestFlowProof(t *testing.T) {
	t.Parallel()
	// Control: claim, criterion fixed before launch, a real run, proof.
	w := flowProofWorld(t)
	if got := w.claimStatus(); got != "UNMEASURED" {
		t.Fatalf("control: a fresh claim is UNMEASURED, got %s", got)
	}
	pass := w.admittedRun(pvPass)
	if got := w.claimStatus(); got != "MEASURED" {
		t.Fatalf("control: an admitted run makes the claim MEASURED, got %s", got)
	}
	if err := w.prove(map[model.ID]string{pass: "supports"}); err != nil {
		t.Fatalf("control: the real run's proof must admit: %v", err)
	}
	claim := w.record(w.claim.RecordID)
	if flowStr(claim, "claim", "status") != "PROVEN" || len(flowList(claim, "claim", "proofs")) != 1 {
		t.Fatalf("control: the claim must read PROVEN with one proof, got %s", flowStr(claim, "claim", "status"))
	}
	w.read("history", string(w.claim.RecordID))
	w.read("state")

	notProven := func(w *flowWorld, what string) {
		t.Helper()
		if got := w.claimStatus(); got == "PROVEN" {
			t.Errorf("%s: the claim reads PROVEN", what)
		}
	}

	t.Run("failing-run-left-out", func(t *testing.T) {
		t.Parallel()
		w := flowProofWorld(t)
		pass := w.admittedRun(pvPass)
		w.admittedRun(pvFail)
		w.refused("proof omitting a failing family member", func() error { return w.prove(map[model.ID]string{pass: "supports"}) })
		notProven(w, "failing run left out")
	})

	t.Run("rejected-run-left-out", func(t *testing.T) {
		t.Parallel()
		w := flowProofWorld(t)
		_, packets, err := w.run(pvProducer(pvFail))
		if err != nil || len(packets) != 2 {
			t.Fatalf("control: a failing run still seals: %v", err)
		}
		if err := w.review("rejected", packets...); err != nil {
			t.Fatalf("control: the reviewer may turn the failing run away: %v", err)
		}
		pass := w.admittedRun(pvPass)
		w.refused("proof omitting a rejected family member (R10.3)", func() error { return w.prove(map[model.ID]string{pass: "supports"}) })
		notProven(w, "rejected run left out")
	})

	t.Run("earlier-revision-failing-run-left-out", func(t *testing.T) {
		t.Parallel()
		w := flowProofWorld(t)
		failed := w.admittedRun(pvFail)
		w.fix(w.criterion.CriterionID, 2)
		pass := w.admittedRun(pvPass)
		w.refused("proof under revision 2 omitting revision 1's failing run (R10.2)", func() error { return w.prove(map[model.ID]string{pass: "supports"}) })
		w.refused("revision 1's failing run offered as support (R10.2)", func() error {
			return w.prove(map[model.ID]string{pass: "supports", failed: "supports"})
		})
		notProven(w, "earlier revision's failing run left out")
		// Accounted for, never support: the same family then proves.
		if err := w.prove(map[model.ID]string{pass: "supports", failed: "inapplicable"}); err != nil || w.claimStatus() != "PROVEN" {
			t.Errorf("control: with the earlier-revision run dispositioned inapplicable the proof must admit: %v", err)
		}
	})

	t.Run("correction-does-not-overcome-a-contradicting-run", func(t *testing.T) {
		t.Parallel()
		w := flowProofWorld(t)
		pass := w.admittedRun(pvPass)
		failed := w.admittedRun(pvFail)
		body := []byte(`{"retracted":"the failing reading"}`)
		w.put("corrections/retract.json", body)
		corrective := pvPin(body, "corrections/retract.json")
		output := pvPin([]byte(pvFail), pvRunPath(failed, "out/result.json"))
		w.mustAdmit(flowLane, &model.Correction{Target: model.CorrectionTarget{Kind: "support", Support: &model.SupportLink{Dependent: w.claim, Evidence: output}},
			AffectedRevisions: []model.RecordRef{w.claim}, Reason: "the failing reading is disputed", CorrectiveRef: corrective})
		for _, disposition := range []string{"inapplicable", "inconclusive", "contradicts"} {
			w.refused("a corrected contradicting run dispositioned "+disposition+" (R10.2)", func() error {
				return w.prove(map[model.ID]string{pass: "supports", failed: disposition})
			})
		}
		notProven(w, "contradicting run under a correction")
	})

	t.Run("criterion-fixed-after-the-run", func(t *testing.T) {
		t.Parallel()
		w := flowProofWorld(t)
		w.admittedRun(pvPass) // control: runs under the first criterion admit
		criterion := w.id()
		w.criterion = model.CriterionRef{Claim: w.claim, CriterionID: criterion, Revision: 1}
		_, events := w.handRun(time.Now(), pvPass)
		packet := w.capture(flowLane, events...)
		time.Sleep(2 * time.Millisecond)
		w.fix(criterion, 1)
		w.refused("a run captured before its criterion was fixed", func() error { return w.review("accepted", packet) })
		notProven(w, "criterion fixed after the run")
	})
}

// DATUM-CONTRACT criterion.fix: "the proof criterion, frozen before
// execution". The gate compares the criterion's admission time with the
// start's authored started_at and nothing else. A run captured (and its
// result seen) BEFORE the criterion existed is admitted when its start simply
// claims a later started_at, and the claim reaches PROVEN on a criterion
// written after the reading. The packet's own captured_at, which intake
// records and the lane does not author, already shows the run came first.
func TestFlowCriterionFrozenIsCheckedAgainstTheCaptureNotTheAuthoredStart(t *testing.T) {
	t.Parallel()
	w := flowProofWorld(t)
	// Control: the honest spelling of the same run is refused.
	criterion := w.id()
	w.criterion = model.CriterionRef{Claim: w.claim, CriterionID: criterion, Revision: 1}
	honestRun, honest := w.handRun(time.Now(), pvPass)
	honestPacket := w.capture(flowLane, honest...)
	// The same run, captured at the same moment, claiming to start an hour later.
	late, dated := w.handRun(time.Now().Add(time.Hour), pvPass)
	datedPacket := w.capture(flowLane, dated...)
	time.Sleep(2 * time.Millisecond)
	w.fix(criterion, 1)
	w.refused("control: a run captured before its criterion", func() error { return w.review("accepted", honestPacket) })
	if err := w.review("rejected", honestPacket); err != nil {
		t.Fatal(err)
	}
	var captured string
	for _, p := range flowList(w.read("intake", "pending"), "intake") {
		if flowStr(p, "command_id") == string(datedPacket) {
			captured = flowStr(p, "packet", "captured_at")
		}
	}
	w.refused("a run captured before its criterion, claiming a later start", func() error { return w.review("accepted", datedPacket) })
	if t.Failed() {
		proofErr := w.prove(map[model.ID]string{late: "supports", honestRun: "inconclusive"})
		t.Logf("packet %s was captured at %s, before criterion %s was admitted, yet its start (started_at an hour ahead) was admitted; proof over it: %v, claim now %s",
			datedPacket, captured, criterion, proofErr, w.claimStatus())
	}
}

// ---- 3. acceptance --------------------------------------------------------------

func TestFlowAcceptance(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("deliver the report")
	delivery := []byte(`{"delivered":"report v1"}`)
	w.put("delivery/report.json", delivery)
	refs, err := json.Marshal([]model.ArtifactRef{pvPin(delivery, "delivery/report.json")})
	if err != nil {
		t.Fatal(err)
	}
	w.put("flow-lane-delivery.json", refs)
	packet, err := w.handback(task.attempt, "success", "--delivery-refs", filepath.Join(w.root, "flow-lane-delivery.json"))
	if err != nil {
		t.Fatalf("control: a success receipt captures: %v", err)
	}
	if err := w.review("accepted", packet); err != nil {
		t.Fatalf("control: a success receipt admits: %v", err)
	}
	r := w.record(task.ref.RecordID)
	if flowStr(r, "task", "status") != "BLOCKED" || flowStr(r, "task", "reasons", 0, "kind") != "awaiting-acceptance" {
		t.Fatalf("success must land awaiting acceptance, got %s %v", flowStr(r, "task", "status"), flowGet(r, "task", "reasons"))
	}
	awaiting := false
	for _, rec := range flowList(w.read("todo"), "preset", "awaiting_acceptance") {
		awaiting = awaiting || flowStr(rec, "fact", "key", "id") == string(task.ref.RecordID)
	}
	if !awaiting {
		t.Error("the succeeded task is not in the awaiting-acceptance queue")
	}

	// The owner's words, captured as a source about this exact revision.
	ruling := []byte(`{"ruling":"accepted: report v1 is delivered"}`)
	w.put("rulings/accept.json", ruling)
	rulingRef := pvPin(ruling, "rulings/accept.json")
	w.mustAdmit(flowLane, &model.SourceIntake{SourceID: w.id(), SourceRef: rulingRef, OriginalDigest: rulingRef.Content.SHA256, Length: rulingRef.Content.Length,
		Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{task.ref}})
	scope := task.spec.Scope
	scope.ContextRefs = []model.RecordRef{task.ref}
	closure := func(witnessed bool) *model.TaskClose {
		c := &model.TaskClose{Task: task.ref, Outcome: model.ClosureSuccess,
			Authority:             model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: rulingRef, Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: scope},
			AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}}
		if witnessed {
			c.AcceptanceWitnessRefs = []model.AcceptanceWitness{{CriterionID: task.spec.AcceptanceCriteria[0].ID, CriterionRevision: 1, WitnessRef: rulingRef}}
			c.DeliveryWitnessRefs = []model.ArtifactRef{pvPin(delivery, "delivery/report.json")}
		}
		return c
	}
	unwitnessed := w.capture("coordinator", closure(false))
	w.refused("task.close success without its witnesses", func() error { return w.review("accepted", unwitnessed) })
	if got := flowStr(w.record(task.ref.RecordID), "task", "status"); got != "BLOCKED" {
		t.Errorf("an unwitnessed closure moved the task to %s", got)
	}
	if err := w.review("rejected", unwitnessed); err != nil {
		t.Fatal(err)
	}
	if err := w.review("accepted", w.capture("coordinator", closure(true))); err != nil {
		t.Fatalf("task.close with the acceptance and delivery witnesses must admit: %v", err)
	}
	r = w.record(task.ref.RecordID)
	if flowStr(r, "task", "status") != "CLOSED" || flowStr(r, "task", "outcome") != "success" {
		t.Errorf("expected CLOSED success, got %s %s", flowStr(r, "task", "status"), flowStr(r, "task", "outcome"))
	}
	for _, rec := range w.openTasks() {
		if flowStr(rec, "fact", "key", "id") == string(task.ref.RecordID) {
			t.Error("a closed task is still listed as owed")
		}
	}
	w.read("history", string(task.ref.RecordID))
}

// ---- 4. recovery ----------------------------------------------------------------

func TestFlowRecovery(t *testing.T) {
	t.Parallel()
	w := flowProofWorld(t)
	pass := w.admittedRun(pvPass)

	// Kill a real `datum run` after its producer is running.
	w.put("tools/run.sh", []byte("touch flow-lane-started\nwhile kill -0 $PPID 2>/dev/null; do sleep 0.05; done\n"))
	cmd := exec.Command(pvDatum(t), "run", "--actor", flowLane, "--attempt-id", string(w.attempt), "--instrument", string(w.instrument.RecordID),
		"--claim", string(w.claim.RecordID), "--claim-revision", "1", "--criterion-id", string(w.criterion.CriterionID), "--criterion-revision", "1",
		"--", "/bin/sh", "tools/run.sh")
	cmd.Dir = w.root
	cmd.Env = append(os.Environ(), "HOME="+w.home)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(w.root, "flow-lane-started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the producer never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if stdout.Len() != 0 {
		t.Fatalf("a killed observer printed packets: %s", stdout.Bytes())
	}
	// Its intent was persisted before launch and is pending in intake.
	var deadStart, dead model.ID
	for _, p := range flowList(w.read("intake", "pending"), "intake") {
		for _, e := range flowList(p, "packet", "events") {
			if flowStr(e, "type") == "invocation.start" && flowStr(p, "disposition") == "pending" {
				deadStart, dead = model.ID(flowStr(p, "command_id")), model.ID(flowStr(e, "data", "envelope", "invocation_id"))
			}
		}
	}
	if deadStart == "" {
		t.Fatal("the killed run left no pending start in intake")
	}
	if err := w.review("accepted", deadStart); err != nil {
		t.Fatalf("control: the killed run's start admits: %v", err)
	}
	w.refused("proof omitting an unsealed family member", func() error { return w.prove(map[model.ID]string{pass: "supports"}) })
	// Listing it does not help: an unsealed member has no result yet, and proof
	// waits for its reconciliation rather than treating it as absent.
	w.refused("proof listing an unsealed family member as inconclusive", func() error {
		return w.prove(map[model.ID]string{pass: "supports", dead: "inconclusive"})
	})
	out, err := w.cli(nil, "reconcile", "--actor", flowLane, "--invocation-id", string(dead), "--reason", "the observer was killed mid-run")
	if err != nil {
		t.Fatalf("control: the dead run reconciles: %v", err)
	}
	if err := w.review("accepted", flowPacket(t, out)); err != nil {
		t.Fatalf("control: the reconciliation seal admits: %v", err)
	}
	for _, run := range flowList(w.read("state"), "preset", "runs") {
		if flowStr(run, "invocation") == string(dead) {
			if flowStr(run, "sealed") != "true" || flowStr(run, "outcome", "state") != "UNKNOWN" || flowStr(run, "observed_at", "state") != "UNKNOWN" {
				t.Errorf("the reconciled run must read sealed with an UNKNOWN outcome and observation, got %v", run)
			}
		}
	}
	w.refused("a reconciled dead runner offered as support", func() error {
		return w.prove(map[model.ID]string{pass: "supports", dead: "supports"})
	})
	if err := w.prove(map[model.ID]string{pass: "supports", dead: "inconclusive"}); err != nil || w.claimStatus() != "PROVEN" {
		t.Fatalf("control: the dead run dispositioned inconclusive, the family proves: %v", err)
	}

	// Delete everything that is not the ledger, the config or intake: every
	// read must answer exactly as before.
	reads := [][]string{{"show"}, {"show", string(w.claim.RecordID)}, {"history"}, {"history", string(w.claim.RecordID)},
		{"now"}, {"state"}, {"todo"}, {"instruments"}, {"intake", "pending"}, {"context"}}
	before := map[string]map[string]any{}
	for _, args := range reads {
		before[strings.Join(args, " ")] = w.read(args...)
	}
	openBefore := w.openTasks()
	ledger := w.ledger()
	entries, err := os.ReadDir(w.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "datum.toml":
		case ".datum":
			inner, err := os.ReadDir(filepath.Join(w.root, ".datum"))
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range inner {
				if r.Name() != "events" {
					if err := os.RemoveAll(filepath.Join(w.root, ".datum", r.Name())); err != nil {
						t.Fatal(err)
					}
				}
			}
		default:
			if err := os.RemoveAll(filepath.Join(w.root, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !reflect.DeepEqual(ledger, w.ledger()) {
		t.Fatal("fixture: deleting generated output touched .datum/events")
	}
	for _, args := range reads {
		if after := w.read(args...); !reflect.DeepEqual(before[strings.Join(args, " ")], after) {
			t.Errorf("datum %s answers differently after generated output was deleted", strings.Join(args, " "))
		}
	}
	if !reflect.DeepEqual(openBefore, w.openTasks()) {
		t.Error("the open (not CLOSED) tasks in `datum show` differ after generated output was deleted")
	}
}

// ---- 5. decisions and correction ----------------------------------------------

func (w *flowWorld) decision() (model.RecordRef, model.ArtifactRef) {
	w.t.Helper()
	open := &model.DecisionOpen{ID: w.id(), Provenance: model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship revision one?", Options: []string{"ship", "hold"}, WaitingActor: model.Actor{ID: "owner"}, Scope: w.scope}}
	w.mustAdmit(flowLane, open)
	ruling := []byte(`{"ruling":"ship revision one"}`)
	w.put("rulings/ship.json", ruling)
	return w.ref(open.ID, 1), pvPin(ruling, "rulings/ship.json")
}

func (w *flowWorld) dispose(decision model.RecordRef, source model.ArtifactRef, quote, authority string) *model.DecisionDispose {
	return &model.DecisionDispose{Decision: decision, Disposition: "approved", Quote: quote, Scope: w.scope,
		Authority: model.Authority{Actor: model.Actor{ID: authority}, SourceRef: source, Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: w.scope}}
}

func TestFlowDecisionAndCorrection(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	decision, source := w.decision()
	d := w.record(decision.RecordID)
	if flowStr(d, "decision", "status") != "OPEN" || flowStr(d, "author", "actor", "id") != flowLane {
		t.Fatalf("control: an opened decision reads OPEN, authored by the lane, got %s by %v", flowStr(d, "decision", "status"), flowGet(d, "author"))
	}
	unnamed := w.dispose(decision, source, "ship revision one", "")
	unnamed.Authority.Actor = model.Actor{UnknownReason: "the scribe did not record who ruled"}
	blank := w.capture("scribe", unnamed)
	w.refused("decision.dispose with no named authority", func() error { return w.review("accepted", blank) })
	if err := w.review("rejected", blank); err != nil {
		t.Fatal(err)
	}
	// An agent records the owner's ruling; the packet author stays the agent.
	if err := w.review("accepted", w.capture("scribe", w.dispose(decision, source, "ship revision one", "owner"))); err != nil {
		t.Fatalf("decision.dispose with a quote and a named authority must admit: %v", err)
	}
	d = w.record(decision.RecordID)
	ruling := flowGet(d, "decision", "dispositions", 0)
	if flowStr(d, "decision", "status") != "DECIDED" || flowStr(ruling, "author", "actor", "id") != "scribe" ||
		flowStr(ruling, "disposition", "authority", "actor", "id") != "owner" || flowStr(ruling, "disposition", "quote") != "ship revision one" {
		t.Errorf("show must read DECIDED with author scribe, authority owner and the quote, got %s %v", flowStr(d, "decision", "status"), ruling)
	}
	found := false
	for _, e := range flowList(w.read("history", string(decision.RecordID)), "history") {
		found = found || flowStr(e, "event", "type") == "decision.dispose" && flowStr(e, "author", "actor", "id") == "scribe"
	}
	if !found {
		t.Error("history of the decision does not show the disposition's author")
	}

	// Correction: current support of a PROVEN claim is lost, history is not.
	p := flowProofWorld(t)
	run := p.admittedRun(pvPass)
	if err := p.prove(map[model.ID]string{run: "supports"}); err != nil {
		t.Fatalf("control: fixture reaches PROVEN: %v", err)
	}
	c := p.record(p.claim.RecordID)
	if flowStr(c, "claim", "status") != "PROVEN" || flowStr(c, "claim", "support", "correction_free") != "TRUE" || flowStr(c, "current_support") == "FALSE" {
		t.Fatalf("control: PROVEN with no correction, got %v", flowGet(c, "claim", "support"))
	}
	body := []byte(`{"missed":"pose-c"}`)
	p.put("corrections/missed.json", body)
	corrective := pvPin(body, "corrections/missed.json")
	p.mustAdmit(flowLane, &model.Correction{Target: model.CorrectionTarget{Kind: "support", Support: &model.SupportLink{Dependent: p.claim, Evidence: corrective}},
		AffectedRevisions: []model.RecordRef{p.claim}, Reason: "the sweep missed a pose", CorrectiveRef: corrective})
	c = p.record(p.claim.RecordID)
	if flowStr(c, "claim", "status") != "PROVEN" || len(flowList(c, "claim", "proofs")) != 1 {
		t.Errorf("a correction erased PROVEN history: %s", flowStr(c, "claim", "status"))
	}
	if flowStr(c, "claim", "support", "correction_free") != "FALSE" || flowStr(c, "current_support") != "FALSE" {
		t.Errorf("a correction must remove current support, got %v, current %s", flowGet(c, "claim", "support"), flowStr(c, "current_support"))
	}
	stated := false
	for _, v := range flowList(p.read("state"), "preset", "claims") {
		if flowStr(v, "ref", "record_id") == string(p.claim.RecordID) {
			stated = flowStr(v, "current_support") == "FALSE" && len(flowList(v, "corrections")) == 1
		}
	}
	if !stated {
		t.Error("STATE does not show the corrected claim's lost support and its correction")
	}
	p.refused("a second proof over an unresolved correction", func() error { return p.prove(map[model.ID]string{run: "supports"}) })
}

// R10.1 revised: an owner act carries "a non-blank verbatim ruling quote and a
// named authority"; the Authority type exists so admission can "resolve an
// actor's exact words", with the selector choosing the ruling inside the
// pinned source. Admission resolves the source and checks the quote is not
// blank, but never compares the quote with the words the selector reads. A
// disposition can therefore record the owner as having said "ship it" while
// citing a pinned ruling that says the opposite, and show/history present that
// quote as the owner's.
func TestFlowDecisionQuoteIsTheAuthoritysWords(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	control, source := w.decision()
	if err := w.review("accepted", w.capture("scribe", w.dispose(control, source, "ship revision one", "owner"))); err != nil {
		t.Fatalf("control: a quote that is the selected ruling admits: %v", err)
	}
	decision, _ := w.decision()
	contrary := []byte(`{"ruling":"do not ship revision one"}`)
	w.put("rulings/hold.json", contrary)
	packet := w.capture("scribe", w.dispose(decision, pvPin(contrary, "rulings/hold.json"), "ship revision one", "owner"))
	w.refused("a disposition quoting words its authority does not say", func() error { return w.review("accepted", packet) })
	if t.Failed() {
		d := w.record(decision.RecordID)
		t.Logf("show now reads %s with quote %q attributed to authority %s, citing a source whose /ruling is %q",
			flowStr(d, "decision", "status"), flowStr(d, "decision", "dispositions", 0, "disposition", "quote"),
			flowStr(d, "decision", "dispositions", 0, "disposition", "authority", "actor", "id"), "do not ship revision one")
	}
}

// ---- the remaining named events, each read back for its effect ---------------

func TestFlowRemainingEvents(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("take over and reconcile")
	amended := task.spec
	amended.Intent = "take over, reconcile, and say so"
	w.mustAdmit(flowLane, &model.TaskAmend{Provenance: model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}},
		Target: task.ref, ExpectedRevision: 1, Replacement: amended})
	r := w.record(task.ref.RecordID)
	if flowStr(r, "task", "revision") != "2" || flowStr(r, "fact", "task", "intent") != amended.Intent || flowStr(r, "task", "status") != "IN FLIGHT" {
		t.Fatalf("task.amend must read revision 2 with the new intent and leave the attempt live, got %s %s", flowStr(r, "task", "revision"), flowStr(r, "task", "status"))
	}
	current := w.ref(task.ref.RecordID, 2)
	stopped := []byte(`{"stopped":"lane confirmed it stopped"}`)
	w.put("confirmations/stopped.json", stopped)
	second := w.id()
	w.mustAdmit("lane2", &model.TaskTakeover{Task: current, Actor: model.Actor{ID: "lane2"}, AttemptID: second, PriorAttemptID: task.attempt,
		StoppedConfirmationRef: pvPin(stopped, "confirmations/stopped.json")})
	holders := map[string]bool{}
	for _, h := range flowList(w.record(task.ref.RecordID), "task", "attempt_holders") {
		holders[flowStr(h, "actor", "id")] = true
	}
	if !holders["lane2"] || !holders[flowLane] {
		t.Errorf("after task.takeover both the new holder and the unanswered prior holder must be visible, got %v", holders)
	}
	stop, err := w.handback(task.attempt, "stopped")
	if err != nil || w.review("accepted", stop) != nil {
		t.Fatalf("control: the prior holder's receipt admits: %v", err)
	}
	out, err := w.cli(nil, "handback", "--command-id", string(w.id()), "--actor", "lane2", "--attempt-id", string(second), "--outcome", "runner-died",
		"--reason", "the runner host rebooted", "--next-action", "reconcile the lost run", "--reconciliation-owed")
	if err != nil || w.review("accepted", flowPacket(t, out)) != nil {
		t.Fatalf("control: the new holder's runner-died receipt admits: %v", err)
	}
	if got := flowStr(w.record(task.ref.RecordID), "task", "status"); got != "BLOCKED" {
		t.Fatalf("reconciliation owed must block, got %s", got)
	}
	hold := w.id()
	w.mustAdmit("lane2", &model.BlockerHold{Task: current, BlockerID: hold, Reason: model.BlockerReconciliation, Actor: model.Actor{ID: "lane2"}, Criterion: "the lost run is reconciled"})
	witness := []byte(`{"reconciled":"no run outlived the reboot"}`)
	w.put("confirmations/reconciled.json", witness)
	w.mustAdmit("lane2", &model.BlockerClear{Task: current, BlockerID: hold, HoldRef: model.BlockerRef{Task: current, BlockerID: hold},
		ResolvingWitness: pvPin(witness, "confirmations/reconciled.json")})
	if got := flowStr(w.record(task.ref.RecordID), "task", "status"); got != "READY" {
		t.Errorf("blocker.clear of the reconciliation must leave the task READY, got %s", got)
	}

	// Decision revision.
	decision, _ := w.decision()
	w.mustAdmit(flowLane, &model.DecisionRevise{Provenance: model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}},
		Target: decision, ExpectedRevision: 1, Replacement: model.DecisionSpec{Question: "ship revision two?", Options: []string{"ship", "hold"}, WaitingActor: model.Actor{ID: "owner"}, Scope: w.scope}})
	d := w.record(decision.RecordID)
	if flowStr(d, "fact", "key", "revision") != "2" || flowStr(d, "decision", "status") != "OPEN" || flowStr(d, "fact", "decision", "question") != "ship revision two?" {
		t.Errorf("decision.revise must read revision 2, OPEN, with the new question; got %v", flowGet(d, "fact", "key"))
	}

	// Trust withdrawal, then claim and instrument revision, on a PROVEN claim.
	p := flowProofWorld(t)
	run := p.admittedRun(pvPass)
	if err := p.prove(map[model.ID]string{run: "supports"}); err != nil {
		t.Fatalf("control: fixture reaches PROVEN: %v", err)
	}
	p.mustAdmit(flowLane, &model.TrustWithdraw{Instrument: p.instrument, Scope: p.scope, RevalidationCondition: "re-run against a known sweep"})
	c := p.record(p.claim.RecordID)
	if flowStr(c, "claim", "status") != "PROVEN" || flowStr(c, "claim", "support", "active_trust") != "FALSE" || flowStr(c, "current_support") != "FALSE" {
		t.Errorf("trust.withdraw must keep PROVEN history and remove current support, got %s %v", flowStr(c, "claim", "status"), flowGet(c, "claim", "support"))
	}
	spec := model.ClaimSpec{Assertion: "every pose is below 0.04 mm", Falsifier: "a pose reaches 0.04 mm", Scope: p.scope, ExternalRefs: []model.ExternalReference{}}
	p.mustAdmit(flowLane, &model.ClaimRevise{Provenance: model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}}, Target: p.claim, ExpectedRevision: 1, Replacement: spec})
	c = p.record(p.claim.RecordID)
	if flowStr(c, "fact", "key", "revision") != "2" || flowStr(c, "claim", "status") != "UNMEASURED" {
		t.Errorf("a revised claim must not borrow revision 1's proof: got revision %s %s", flowStr(c, "fact", "key", "revision"), flowStr(c, "claim", "status"))
	}
	record, _ := p.record(p.instrument.RecordID)["fact"].(map[string]any)
	instrument := model.InstrumentSpec{}
	raw, _ := json.Marshal(record["instrument"])
	if err := json.Unmarshal(raw, &instrument); err != nil {
		t.Fatal(err)
	}
	instrument.BlindTo = "unmeasured poses and any pose outside the fixture sweep"
	p.mustAdmit(flowLane, &model.InstrumentRevise{Provenance: model.Provenance{Author: model.Actor{ID: flowLane}, SourceRefs: []model.ArtifactRef{}}, Target: p.instrument, ExpectedRevision: 1, Replacement: instrument})
	listed := false
	for _, v := range flowList(p.read("instruments"), "preset", "instruments") {
		listed = listed || flowStr(v, "ref", "record_id") == string(p.instrument.RecordID) && flowStr(v, "ref", "revision") == "2" && flowStr(v, "blind_to") == instrument.BlindTo
	}
	if !listed {
		t.Error("INSTRUMENTS does not show the revised instrument's new blind spot")
	}
}
