package acceptance_test

// The guide, executed as written. This file runs the main workflows of
// `whosaidso help` against a temporary project by taking each command line
// from the built binary's own help text (never a hand copy), filling only the
// guide's placeholders from a small explicit table, and carrying every id from
// the DEFAULT (non --json) output of an earlier step. It asserts the exit
// status the guide documents and the statuses the four reads report.
//
// What belongs here: guide-extracted recipes and the judgment an agent must
// author (texts, file contents). What does not: hand-built events, --json
// decoding, or recipes typed into the test; a step the guide does not show
// fails the walk as a finding, never supplied by the test.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const wtJudgment = "walkthrough judgment"

type wtWalk struct {
	t          *testing.T
	root, home string
	vars       map[string]string // the placeholder table
	shellVars  map[string]string // $NAME values the guide itself defines
}

type wtTok struct {
	s      string
	quoted bool
}

// wtLex reads one shell command from s: quotes, $VAR in double quotes,
// backslash-newline continuation, "> FILE" redirection. The command ends at a
// newline, a comment, a run of two spaces (the guide's aligned descriptions),
// a token opening "(", or a "." or "," that ends a clause of prose.
func wtLex(s string, env map[string]string) []wtTok {
	var toks []wtTok
	var cur strings.Builder
	have, quoted := false, false
	flush := func() {
		if have {
			toks = append(toks, wtTok{cur.String(), quoted})
		}
		cur.Reset()
		have, quoted = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '\n':
			flush()
			for i+2 < len(s) && s[i+2] == ' ' {
				i++
			}
			i++
		case c == '\n':
			flush()
			return toks
		case c == ' ':
			if i+1 < len(s) && s[i+1] == ' ' {
				flush()
				return toks
			}
			flush()
		case c == '#' && !have:
			flush()
			return toks
		case c == '(' && !have:
			return toks
		case c == ',' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n'):
			flush()
			return toks
		case c == '.' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n') && (quoted || !strings.HasSuffix(cur.String(), "..")):
			flush()
			return toks
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			cur.WriteString(s[i+1 : i+1+j])
			have, quoted, i = true, true, i+1+j
		case c == '"':
			have, quoted = true, true
			for i++; i < len(s) && s[i] != '"'; i++ {
				switch {
				case s[i] == '\\' && s[i+1] == '\n':
					i++
				case s[i] == '$':
					m := regexp.MustCompile(`^[A-Z_]+`).FindString(s[i+1:])
					cur.WriteString(env[m])
					i += len(m)
				default:
					cur.WriteByte(s[i])
				}
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	flush()
	return toks
}

// wtGuide returns the text of `whosaidso help topic`.
func (w *wtWalk) guide(topic string) string {
	out, _, code := w.exec(nil, "", "help", topic)
	if code != 0 {
		w.t.Fatalf("whosaidso help %s exited %d", topic, code)
	}
	return out
}

// recipe finds the guide's command line in topic that starts with prefix and
// lexes it. A guide that no longer shows the line fails the walk: the step it
// taught is gone.
func (w *wtWalk) recipe(topic, prefix string) []wtTok {
	text := w.guide(topic)
	i := strings.Index(text, prefix)
	if i < 0 {
		w.t.Fatalf("whosaidso help %s no longer shows a line starting %q; the walkthrough follows the guide, so this step cannot be taken", topic, prefix)
	}
	return wtLex(text[i:], w.shellVars)
}

var wtPlaceholder = regexp.MustCompile(`\b[A-Z][A-Z_]*\b`)

// fill substitutes the placeholder table into a recipe. A bare unquoted "..."
// is repetition and is dropped; "..." quoted or inside a word is judgment text.
// An optional [group] is kept only when the table fills its placeholders. An
// uppercase word the table cannot fill stops the walk: that is a finding.
func (w *wtWalk) fill(toks []wtTok, extra map[string]string) (args []string, stdout string) {
	w.t.Helper()
	table := map[string]string{}
	for k, v := range w.vars {
		table[k] = v
	}
	for k, v := range extra {
		table[k] = v
	}
	sub := func(s string) string {
		s = strings.ReplaceAll(s, "...", wtJudgment)
		return wtPlaceholder.ReplaceAllStringFunc(s, func(m string) string {
			v, ok := table[m]
			if !ok {
				w.t.Fatalf("guide recipe token %q: placeholder %s has no value in the walkthrough's table; an agent following the guide would not know what to put there", s, m)
			}
			return v
		})
	}
	for i := 1; i < len(toks); i++ { // toks[0] is "whosaidso"
		tk := toks[i]
		switch {
		case tk.s == ">" && !tk.quoted:
			stdout, i = toks[i+1].s, i+1
			continue
		case tk.s == "..." && !tk.quoted:
			continue
		case strings.HasPrefix(tk.s, "[") && !tk.quoted:
			var group []string
			for ; i < len(toks); i++ {
				group = append(group, strings.Trim(toks[i].s, "[]"))
				if strings.HasSuffix(toks[i].s, "]") {
					break
				}
			}
			keep := true
			for _, g := range group {
				for _, m := range wtPlaceholder.FindAllString(g, -1) {
					if _, ok := table[m]; !ok {
						keep = false
					}
				}
			}
			if keep {
				for _, g := range group {
					if g != "..." {
						args = append(args, sub(g))
					}
				}
			}
			continue
		}
		if v, ok := table[tk.s]; ok { // a whole-token placeholder: a choice, or ARGV's words
			if strings.HasPrefix(v, "argv:") {
				args = append(args, strings.Fields(strings.TrimPrefix(v, "argv:"))...)
			} else {
				args = append(args, v)
			}
			continue
		}
		args = append(args, sub(tk.s))
	}
	return args, stdout
}

func (w *wtWalk) exec(stdin []byte, stdoutFile string, args ...string) (string, string, int) {
	w.t.Helper()
	cmd := exec.Command(pvWhoSaidSo(w.t), args...)
	cmd.Dir, cmd.Stdin = w.root, bytes.NewReader(stdin)
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "WHOSAIDSO_") && !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+w.home, "WHOSAIDSO_HOME="+filepath.Join(w.home, ".whosaidso"), "WHOSAIDSO_ACTOR=walker")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		w.t.Fatal(err)
	}
	if stdoutFile != "" {
		pvPut(w.t, w.root, stdoutFile, so.Bytes())
	}
	return so.String(), se.String(), code
}

// step runs one guide recipe and requires the exit status the guide gives.
func (w *wtWalk) step(why string, want int, toks []wtTok, extra map[string]string) (string, string) {
	w.t.Helper()
	args, file := w.fill(toks, extra)
	so, se, code := w.exec(nil, file, args...)
	w.t.Logf("$ whosaidso %s  -> exit %d\n%s%s", strings.Join(args, " "), code, wtShort(so, file), wtHead(se))
	if code != want {
		w.t.Fatalf("%s: the guide's line `whosaidso %s` should exit %d, got %d:\n%s%s", why, strings.Join(args, " "), want, code, so, se)
	}
	return so, se
}

func wtHead(s string) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 6 {
		return strings.Join(lines[:3], "") + "  ...\n" + strings.Join(lines[len(lines)-3:], "")
	}
	return s
}

func wtShort(so, file string) string {
	if file != "" {
		return "(stdout to " + file + ")\n"
	}
	return so
}

// carry reads the id labelled label from default output; the guide says the
// next step needs it there.
func (w *wtWalk) carry(what, out string, pattern string) string {
	w.t.Helper()
	m := regexp.MustCompile(pattern + `([0-9A-HJKMNP-TV-Z]{26})`).FindStringSubmatch(out)
	if m == nil {
		w.t.Fatalf("the guide says the %s is printed here, matching %q; the default output has none, so an agent cannot take the next step:\n%s", what, pattern, out)
	}
	return m[1]
}

// edit is the agent's hand edit of a template file: set each judgment field.
func (w *wtWalk) edit(file string, sets map[string]any) {
	w.t.Helper()
	var events []map[string]any
	if err := json.Unmarshal(wtRead(w.t, w.root, file), &events); err != nil || len(events) != 1 {
		w.t.Fatalf("%s is not the one-event array the template promised: %v", file, err)
	}
	for path, v := range sets {
		node := events[0]["data"]
		parts := strings.Split(path, ".")
		for i, p := range parts {
			name, idx := p, -1
			if j := strings.IndexByte(p, '['); j >= 0 {
				name, idx = p[:j], int(p[j+1]-'0')
			}
			m := node.(map[string]any)
			last := i == len(parts)-1
			switch {
			case last && idx < 0 && v == nil:
				delete(m, name)
			case last && idx < 0:
				m[name] = v
			case last:
				m[name].([]any)[idx] = v
			case idx < 0:
				node = m[name]
			default:
				node = m[name].([]any)[idx]
			}
		}
	}
	body, _ := json.MarshalIndent(events, "", "  ")
	if bytes.Contains(body, []byte(`"<`)) {
		w.t.Fatalf("walkthrough edit left a placeholder in %s:\n%s", file, body)
	}
	pvPut(w.t, w.root, file, body)
}

func wtRead(t *testing.T, root, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wtTask is the agent's hand edit of `template task.create > task.json`: the
// judgment the guide says to author, and the optional keys it leaves out.
func (w *wtWalk) wtTask(file, intent string) {
	w.edit(file, map[string]any{
		"provenance.source_refs":                []any{},
		"spec.intent":                           intent,
		"spec.subject":                          intent,
		"spec.scope.source_paths":               []any{},
		"spec.scope.context_refs":               []any{},
		"spec.scope.applies_when":               "the walkthrough fixture",
		"spec.scope.limitations":                "a temp project",
		"spec.non_goals":                        []any{"anything the guide does not teach"},
		"spec.acceptance_criteria[0].criterion": "the walk reaches the end",
		"spec.context_refs":                     []any{},
		"spec.constraint_refs":                  []any{},
		"spec.prerequisites":                    []any{},
		"spec.next_actor":                       map[string]any{"id": "walker"},
		"spec.accepter":                         nil,
		"spec.progress":                         nil,
	})
}

func TestHelpWalkthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only; Windows is out of scope")
	}
	w := &wtWalk{t: t, root: t.TempDir(), home: t.TempDir(), vars: map[string]string{}, shellVars: map[string]string{}}
	v := w.vars
	v["TEXT"] = wtJudgment // a flag's authored text, as "..." is
	views := func(why string, extra map[string]string, prefix string) string {
		out, _ := w.step(why, 0, w.recipe("views", prefix), extra)
		if first, _, _ := strings.Cut(out, "\n"); !strings.Contains(first, "watermark") {
			t.Errorf("help views: every answer opens with its watermark; %s opens %q", why, first)
		}
		return out
	}

	// ---- setup: whosaidso.toml as the loop topic writes it, then bind home.
	loop := w.guide("loop")
	var toml []string
	for _, line := range strings.Split(loop, "\n") {
		if m := regexp.MustCompile(`^\s+((id|ledger) = '[^']*')`).FindStringSubmatch(line); m != nil {
			toml = append(toml, m[1])
		}
	}
	if len(toml) != 2 {
		t.Fatalf("help loop should show the two whosaidso.toml keys, found %q", toml)
	}
	pvPut(t, w.root, "whosaidso.toml", []byte(strings.Join(toml, "\n")+"\n"))
	v["P"] = regexp.MustCompile(`'([^']*)'`).FindStringSubmatch(toml[0])[1]
	w.step("an unbound project refuses reads (help home)", 1, w.recipe("views", "whosaidso todo"), nil)
	out, _ := w.step("bind the home (help loop)", 0, w.recipe("loop", "whosaidso home PATH"), map[string]string{"PATH": "."})
	if !strings.Contains(out, "bound "+v["P"]) {
		t.Fatalf("home PATH should report the binding of %s:\n%s", v["P"], out)
	}

	// ---- create a task: template > task.json, fill, capture, admit.
	w.step("template (help loop)", 0, w.recipe("loop", "whosaidso template task.create >"), nil)
	w.wtTask("task.json", "walk the accept workflow")
	out, se := w.step("capture (help loop)", 0, w.recipe("loop", "whosaidso capture --events task.json  "), nil)
	v["PACKET"] = w.carry("captured packet id", out, `captured `)
	v["T"] = w.carry("new task id", se, `task\.create id = `)
	out, _ = w.step("admit (help loop)", 0, w.recipe("loop", "whosaidso admit --outcome accepted"), nil)
	if !strings.HasPrefix(out, "admitted "+v["PACKET"]) {
		t.Fatalf("admit should acknowledge %s:\n%s", v["PACKET"], out)
	}

	// ---- start, handback, admit the receipt (help accept).
	_, se = w.step("start (help accept)", 0, w.recipe("accept", "whosaidso template task.start"), nil)
	v["A"] = w.carry("attempt_id start prints", se, `attempt_id = `)
	out, _ = w.step("handback (help accept)", 0, w.recipe("accept", "whosaidso handback"), nil)
	v["PACKET"] = w.carry("receipt handback prints", out, `captured `)
	w.step("admit the receipt (help accept)", 0, w.recipe("accept", "whosaidso admit"), nil)

	// ---- withhold acceptance: hold, a refused close (4), reject it, clear, close.
	pvPut(t, w.root, "evidence.txt", []byte("the walk reached the end\n"))
	pvPut(t, w.root, "delivered.txt", []byte("the delivered file\n"))
	v["EVIDENCE"], v["DELIVERED"], v["WHO"] = "evidence.txt", "delivered.txt", "owner"
	_, se = w.step("hold (help accept)", 0, w.recipe("accept", "whosaidso template blocker.hold"), nil)
	v["HOLD"] = w.carry("hold id (help accept: it prints blocker_id = HOLD)", se, `blocker_id = `)
	close := w.recipe("accept", "whosaidso template task.close")
	out, se = w.step("close while held is partial success (help admit)", 4, close, nil)
	v["PACKET"] = w.carry("pending packet of the refused close", out+se, `captured `)
	if !strings.Contains(out+se, "retry: whosaidso admit") {
		t.Errorf("help admit: a refused --admit prints the retry command; none in:\n%s%s", out, se)
	}
	out, _ = w.step("record the refusal (help admit)", 0, w.recipe("admit", "whosaidso admit --outcome"), map[string]string{"accepted|rejected|correction-requested": "rejected"})
	if !strings.HasPrefix(out, "rejected "+v["PACKET"]) {
		t.Fatalf("admit --outcome rejected should acknowledge %s:\n%s", v["PACKET"], out)
	}
	w.step("clear (help accept)", 0, w.recipe("accept", "whosaidso template blocker.clear"), nil)
	w.step("close (help accept)", 0, close, nil)
	closed := v["T"]

	// ---- the one-step form (help loop), then an attempt for the runs.
	w.step("template (help loop)", 0, w.recipe("loop", "whosaidso template task.create >"), nil)
	w.wtTask("task.json", "walk the proof workflow")
	_, se = w.step("capture and admit at once (help loop)", 0, w.recipe("loop", "whosaidso capture --events task.json --admit"), nil)
	v["T"] = w.carry("new task id", se, `task\.create id = `)
	_, se = w.step("start (help accept)", 0, w.recipe("accept", "whosaidso template task.start"), nil)
	v["A"] = w.carry("attempt_id start prints", se, `attempt_id = `)

	// ---- the instrument I and the claims C the proof needs (help proof).
	pvPut(t, w.root, "measure.json", []byte(`{"step": {"value": 0.75, "unit": "world units", "population": "the quarter-second step", "denominator": "one step"}}`+"\n"))
	pvPut(t, w.root, "validation.json", []byte(`{"validated": "against a known step"}`+"\n"))
	v["TOOL"], v["VALIDATION"] = "measure.json", "validation.json"
	_, se = w.step("instrument (help proof)", 0, w.recipe("proof", "whosaidso template instrument.declare"), nil)
	v["I"] = w.carry("instrument id (help proof: minted id = ID)", se, `minted\s+id = `)
	claims := map[string]string{}
	for _, name := range []string{"proven", "refuted"} {
		_, se = w.step("claim (help proof)", 0, w.recipe("proof", "whosaidso template claim.assert --set"), nil)
		claims[name] = w.carry("claim id (help proof: minted id = ID)", se, `minted\s+id = `)
	}

	// ---- proof (help proof), twice: C measured as claimed reads PROVEN; C2,
	// whose criterion the same measurement fails, is recorded REFUTED.
	v["FILE"], v["PTR"], v["EXAMPLE"], v["ARGV"] = "measure.json", "step", "measure.json", "argv:cat measure.json"
	pvPut(t, w.root, "long.json", []byte(`{"step": {"value": 0.8, "unit": "world units", "population": "the quarter-second step", "denominator": "one step"}}`+"\n"))
	pvPut(t, w.root, "unitless.json", []byte(`{"step": {"value": 0.75}}`+"\n"))
	for _, c := range []struct {
		claim           string
		target          float64
		preview         int
		disposition     string
		verdict, status string
	}{{claims["proven"], 0.75, 0, "supports", "supports", "PROVEN"}, {claims["refuted"], 0.5, 1, "contradicts", "refutes", "REFUTED"}} {
		on := map[string]string{"C": c.claim}
		// 1. fix the criterion from an example (help proof).
		_, se = w.step("criterion from an example (help proof)", 0, w.recipe("proof", "whosaidso template criterion.fix"), on)
		if !strings.Contains(se, "capture it with --blob "+v["FILE"]) {
			t.Errorf("help proof: the criterion's capture needs --blob FILE, which the template prints; its notes do not name --blob %s:\n%s", v["FILE"], se)
		}
		w.edit("crit.json", map[string]any{ // operator, target and reducer are yours (help proof)
			"expression.operator":     "eq",
			"expression.target":       map[string]any{"type": "number", "number": c.target},
			"expression.reducer":      "all",
			"expression.empty_result": false,
			"source_refs":             []any{},
		})
		checkCrit := w.recipe("check", "whosaidso check criterion")
		w.step("criterion preview over its example (help check: 0 TRUE, 1 FALSE)", c.preview, checkCrit, nil)
		if c.preview == 0 {
			w.step("criterion preview FALSE exits 1 (help check)", 1, checkCrit, map[string]string{"RUN_OUTPUT": "long.json"})
			w.step("criterion preview UNKNOWN exits 3 (help check)", 3, checkCrit, map[string]string{"RUN_OUTPUT": "unitless.json"})
		}
		// "admit it in an EARLIER bundle than any run".
		w.step("admit the criterion (help proof)", 0, w.recipe("proof", "whosaidso capture --events crit.json"), nil)

		// 2. run, then admit its start and seal.
		out, _ = w.step("run (help proof)", 0, w.recipe("proof", "whosaidso run --attempt-id"), on)
		run := w.carry("run id", out, `run `)
		start, seal := w.carry("run's start packet", out, `start `), w.carry("run's seal packet", out, `seal `)
		w.step("admit start and seal (help admit)", 0, w.recipe("admit", "whosaidso admit --outcome"),
			map[string]string{"accepted|rejected|correction-requested": "accepted", "PACKET": "argv:" + start + " " + seal})

		// 3. the family, a skeleton, the judgment, a dry run, admit.
		out, _ = w.step("proof family (help proof)", 0, w.recipe("proof", "whosaidso check admission --family C"), on)
		if !strings.Contains(out, run) {
			t.Fatalf("check admission --family should list run %s:\n%s", run, out)
		}
		w.step("proof skeleton (help proof)", 0, w.recipe("proof", "whosaidso template proof.admit --claim C"), on)
		w.edit("proof.json", map[string]any{
			"evidence[0].disposition": c.disposition,
			"evidence[0].reason":      "the run read the step",
			"evidence[0].code_change": nil,
			"judgment.reason":         "one exact run with a validated instrument",
			"verdict":                 c.verdict,
		})
		w.step("proof dry run (help proof)", 0, w.recipe("proof", "whosaidso check admission --events proof.json"), nil)
		w.step("admit the proof (help proof)", 0, w.recipe("proof", "whosaidso capture --events proof.json"), nil)
		out = views("show the claim (help views)", map[string]string{"RECORD_ID": c.claim}, "whosaidso show [RECORD_ID]")
		if !regexp.MustCompile(`CLAIM ` + c.claim + ` .*\b` + c.status + `\b`).MatchString(out) {
			t.Errorf("after a %s proof, show %s should read %s:\n%s", c.verdict, c.claim, c.status, out)
		}
	}

	// ---- the other check modes' exits (help check).
	w.step("admission dry run of an already-admitted task would refuse: exit 1 (help check)", 1,
		w.recipe("check", "whosaidso check admission --events ev.json"), map[string]string{"ev.json": "task.json"})
	out, _ = w.step("disposal preview (help check)", 0, w.recipe("check", "whosaidso check disposal --digest"), map[string]string{"SHA256": wtSHA(wtRead(t, w.root, "evidence.txt"))})
	if !strings.Contains(out, "watermark") {
		t.Errorf("help check: each check prints its scope (at watermark W) first:\n%s", out)
	}

	// ---- capture a source (help sources): an owner's message, from outside the project.
	outside := t.TempDir()
	pvPut(t, outside, "owner.txt", []byte("close the walkthrough task\n"))
	sources := w.guide("sources")
	ref := regexp.MustCompile(`REF='([^']*)'`).FindStringSubmatch(sources)
	if ref == nil {
		t.Fatalf("help sources should define REF for its source.intake recipe:\n%s", sources)
	}
	w.shellVars["REF"] = ref[1]
	_, se = w.step("capture a source (help sources)", 0, w.recipe("sources", "whosaidso template source.intake"),
		map[string]string{"PATH": filepath.Join(outside, "owner.txt"), "ID": closed})
	src := w.carry("new source id", se, `minted\s+\S*id = `)

	// ---- the four reads (help views) return what the walk made.
	out = views("todo (help views)", nil, "whosaidso todo")
	if !strings.Contains(out, "in flight:") || strings.Contains(out, closed) {
		t.Errorf("todo should list the proof task in flight and owe nothing for the closed task %s:\n%s", closed, out)
	}
	out = views("continue (help views)", map[string]string{"RECORD_ID": closed}, "whosaidso continue RECORD_ID")
	if !strings.Contains(out, closed) || !strings.Contains(out, "CLOSED") {
		t.Errorf("continue %s should resume the closed task as CLOSED:\n%s", closed, out)
	}
	out = views("show (help views)", map[string]string{"RECORD_ID": closed}, "whosaidso show [RECORD_ID]")
	if !regexp.MustCompile(`TASK ` + closed + ` .*\bCLOSED\b`).MatchString(out) {
		t.Errorf("show %s should read CLOSED:\n%s", closed, out)
	}
	out = views("history (help views)", map[string]string{"RECORD_ID": closed}, "whosaidso history [RECORD_ID]")
	for _, e := range []string{"task.create", "task.start", "attempt.terminal", "blocker.hold", "blocker.clear", "task.close"} {
		if !strings.Contains(out, e) {
			t.Errorf("history %s should list its %s:\n%s", closed, e, out)
		}
	}
	// help views: a source's source_id reads with show and history; bare show lists sources.
	out = views("show the source (help views)", map[string]string{"RECORD_ID": src}, "whosaidso show [RECORD_ID]")
	if first, _, _ := strings.Cut(out, "\n"); !strings.Contains(first, "KNOWN") || strings.Contains(first, "UNKNOWN") || !strings.Contains(out, src) {
		t.Errorf("help views says show reads a source's source_id; show %s should answer KNOWN and list the source:\n%s", src, out)
	}
	out = views("history of the source (help views)", map[string]string{"RECORD_ID": src}, "whosaidso history [RECORD_ID]")
	if !strings.Contains(out, "source.intake") {
		t.Errorf("help views says history reads a source's source_id; history %s should list its source.intake:\n%s", src, out)
	}
	if out = views("bare show (help views)", nil, "whosaidso show [RECORD_ID]"); !strings.Contains(out, src) {
		t.Errorf("help views says bare show lists sources; source %s is missing:\n%s", src, out)
	}
}

func wtSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
