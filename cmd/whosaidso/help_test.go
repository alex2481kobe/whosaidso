package main

// Help honesty: help names no verb, mode, topic or flag the CLI
// lacks, and the CLI has no verb help omits. The forms of help, flags after
// positional ids, and the JSON error object are tested here too.

import (
	"encoding/json"
	"flag"
	"regexp"
	"strings"
	"testing"
)

var (
	helpVerbRef = regexp.MustCompile("(?:^|[\\s`(])whosaidso ([a-z]+)(?: ([a-z]+))?")
	helpFlagRef = regexp.MustCompile(`(?:^|[^a-z-])--([a-z][a-z-]*)`)
	// helpGitRef is a git command in a recipe: its flags are git's.
	helpGitRef = regexp.MustCompile(`(?:^|[\s(])git [a-z]+`)
)

// flagsOf is the set of flags a verb's FlagSet really declares.
func flagsOf(v *verb) map[string]bool {
	fs := flag.NewFlagSet(v.name, flag.ContinueOnError)
	v.define(fs)
	out := map[string]bool{"help": true}
	fs.VisitAll(func(f *flag.Flag) { out[f.Name] = true })
	return out
}

// helpDishonesty lists every `whosaidso VERB`, `whosaidso help NAME` and --flag in
// text that the CLI does not have. A flag on a line after `whosaidso VERB`
// must be that verb's, and one after `git CMD` is git's and not checked; any
// other flag must be some verb's.
func helpDishonesty(text string) []string {
	anyFlag := map[string]bool{}
	for i := range registry {
		for f := range flagsOf(&registry[i]) {
			anyFlag[f] = true
		}
	}
	var bad []string
	for _, line := range strings.Split(text, "\n") {
		type ref struct {
			at int
			v  *verb
		}
		var refs []ref
		for _, m := range helpVerbRef.FindAllStringSubmatchIndex(line, -1) {
			first := line[m[2]:m[3]]
			second := ""
			if m[4] >= 0 {
				second = line[m[4]:m[5]]
			}
			v, _ := lookup([]string{first, second})
			if v == nil || v.name != first+" "+second {
				v, _ = lookup([]string{first})
			}
			switch {
			case v == nil:
				bad = append(bad, "verb "+first+" in: "+line)
			case v.name == "help" && second != "":
				name := second
				if len(groupModes(second)) > 0 {
					if rest := strings.Fields(line[m[5]:]); len(rest) > 0 {
						if mv, _ := lookup([]string{second, rest[0]}); mv != nil {
							name += " " + rest[0]
						}
					}
				}
				if _, ok := helpFor(name); !ok {
					bad = append(bad, "help topic "+name+" in: "+line)
				}
			}
			if v != nil {
				refs = append(refs, ref{m[0], v})
			}
		}
		gits := helpGitRef.FindAllStringIndex(line, -1)
		for _, m := range helpFlagRef.FindAllStringSubmatchIndex(line, -1) {
			name := line[m[2]:m[3]]
			var owner *verb
			at := -1
			for _, r := range refs {
				if r.at < m[2] {
					owner, at = r.v, r.at
				}
			}
			gitOwns := false
			for _, g := range gits {
				if g[0] < m[2] && g[0] > at {
					gitOwns = true
				}
			}
			if gitOwns {
				continue
			}
			if owner != nil && owner.name != "help" && !flagsOf(owner)[name] || (owner == nil || owner.name == "help") && !anyFlag[name] {
				bad = append(bad, "flag --"+name+" in: "+line)
			}
		}
	}
	return bad
}

func TestHelpNamesOnlyWhatTheCLIHas(t *testing.T) {
	all := wholeGuide() + bareSummary
	for i := range registry {
		all += verbHelp(&registry[i])
	}
	if bad := helpDishonesty(all); len(bad) > 0 {
		t.Fatalf("help names what the CLI lacks:\n%s", strings.Join(bad, "\n"))
	}
	// Control: the checker sees a removed verb, an unknown mode, a missing
	// topic, and a flag on the wrong verb, also after a git command.
	for _, lie := range []string{"run whosaidso now", "whosaidso check proof --events x", "whosaidso help focus", "whosaidso show --digest X", "use --full",
		"git fetch --all && whosaidso todo --digest X"} {
		if len(helpDishonesty(lie)) == 0 {
			t.Fatalf("the honesty check passed %q", lie)
		}
	}
}

func TestEveryVerbIsInHelpAndAnswersHelp(t *testing.T) {
	guideText, _ := helpFor("all")
	idx := index()
	if !strings.HasPrefix(guideText, idx) || !strings.HasPrefix(idx, "TOPICS\n") {
		t.Fatalf("whosaidso help must open with the keyword index:\n%s", guideText[:200])
	}
	for i := range registry {
		v := &registry[i]
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(v.name) + ` .*whosaidso help ` + regexp.QuoteMeta(v.name) + `$`).MatchString(idx) {
			t.Errorf("the index omits verb %q", v.name)
		}
		usage := verbHelp(v)
		if !strings.Contains(guideText, usage) {
			t.Errorf("the whole guide omits the usage of %q", v.name)
		}
		for f := range flagsOf(v) {
			if f != "help" && !regexp.MustCompile(`(?m)^  --`+regexp.QuoteMeta(f)+`( |$)`).MatchString(usage) {
				t.Errorf("whosaidso help %s omits its flag --%s", v.name, f)
			}
		}
		out, errs, code := cliRun(t, t.TempDir(), nil, "", append(strings.Fields(v.name), "--help")...)
		if code != 0 || out != usage || errs != "" {
			t.Errorf("whosaidso %s --help must print its usage once, got %d %q %q", v.name, code, out, errs)
		}
		if out, _, code := cliRun(t, t.TempDir(), nil, "", append([]string{"help"}, strings.Fields(v.name)...)...); code != 0 || !strings.Contains(out, usage) {
			t.Errorf("whosaidso help %s must print its usage, got %d", v.name, code)
		}
	}
	for _, topic := range guide {
		if !strings.Contains(idx, "  "+topic.name+" ") || !strings.Contains(guideText, topicHelp(topic)) {
			t.Errorf("topic %q is missing from the index or the guide", topic.name)
		}
	}
}

func TestHelpForms(t *testing.T) {
	dir := t.TempDir()
	if out, _, code := cliRun(t, dir, nil, ""); code != 0 || !strings.HasSuffix(out, "run `whosaidso help`.\n") || strings.Count(out, "\n") != 5 {
		t.Fatalf("bare whosaidso must print a five-line summary ending with whosaidso help, got %d %q", code, out)
	}
	indexText, _, code := cliRun(t, dir, nil, "", "help")
	if code != 0 || !strings.HasPrefix(indexText, "TOPICS\n") || !strings.Contains(indexText, "whosaidso help all") || strings.Contains(indexText, "VERB REFERENCE") {
		t.Fatalf("whosaidso help must print the index: %d", code)
	}
	if flagForm, _, _ := cliRun(t, dir, nil, "", "--help"); flagForm != indexText {
		t.Fatal("whosaidso --help must print the same index")
	}
	if whole, _, code := cliRun(t, dir, nil, "", "help", "all"); code != 0 || !strings.Contains(whole, "VERB REFERENCE") {
		t.Fatalf("whosaidso help all must print the complete guide: %d", code)
	}
	if out, _, code := cliRun(t, dir, nil, "", "help", "proof"); code != 0 || !strings.HasPrefix(out, "PROOF: ") || strings.Contains(out, "TOPICS") {
		t.Fatalf("whosaidso help TOPIC must print one part, got %d %q", code, out)
	}
	if out, _, code := cliRun(t, dir, nil, "", "help", "check"); code != 0 || !strings.HasPrefix(out, "CHECK: ") || strings.Count(out, "whosaidso check ") < 6 {
		t.Fatalf("whosaidso help check must print the topic and every mode's usage, got %d %q", code, out)
	}
	for _, args := range [][]string{{"help", "focus"}, {"check"}, {"check", "proof"}, {"nope"}} {
		if out, errs, code := cliRun(t, dir, nil, "", args...); code != 2 || out != "" || !strings.Contains(errs, "whosaidso help") {
			t.Fatalf("%v must be a usage error pointing at whosaidso help, got %d %q %q", args, code, out, errs)
		}
	}
	if out, _, code := cliRun(t, dir, nil, "", "check", "--help"); code != 0 || strings.Count(out, "\nwhosaidso check ")+strings.Count(out[:16], "whosaidso check ") != 5 {
		t.Fatalf("whosaidso check --help must print its five modes once each, got %d %q", code, out)
	}
}

func TestBareHelpIsIndexAndAllIsCompleteGuide(t *testing.T) {
	bare, ok := helpFor("")
	if !ok || !strings.HasPrefix(bare, index()) || !strings.Contains(bare, "whosaidso help all") || strings.Contains(bare, "VERB REFERENCE") || strings.Contains(bare, topicHelp(guide[0])) {
		t.Fatalf("bare help must be the keyword index and discovery line, got %q", bare)
	}
	all, ok := helpFor("all")
	if !ok || !strings.Contains(all, "VERB REFERENCE") || !strings.Contains(all, topicHelp(guide[0])) || !strings.Contains(all, topicHelp(guide[1])) {
		t.Fatalf("help all must carry the complete guide, got %q", all)
	}
	sections := strings.SplitN(bare, "VERBS\n", 2)
	if len(sections) != 2 {
		t.Fatal("index has no verb section")
	}
	for _, v := range registry {
		if strings.Count(sections[1], "whosaidso help "+v.name+"\n") != 1 {
			t.Fatalf("index must list verb %q once", v.name)
		}
	}
	for _, topic := range guide {
		if strings.Count(sections[0], "whosaidso help "+topic.name+"\n") != 1 {
			t.Fatalf("index must list topic %q once", topic.name)
		}
	}
	t.Logf("help on synthetic empty project: old full guide %d bytes; index %d bytes", len(all), len(bare))
}

// Someone who merged branches and sees a set-aside bundle in todo must find,
// in help, that merging is normal, what set aside means and what to do.
func TestHelpExplainsBranchesAndSetAsideBundles(t *testing.T) {
	out, _, code := cliRun(t, t.TempDir(), nil, "", "help", "branches")
	if code != 0 || !strings.HasPrefix(out, "BRANCHES: ") {
		t.Fatalf("whosaidso help branches must print the topic, got %d %q", code, out)
	}
	for _, want := range []string{"merge it with a merge commit", "SET ASIDE", "todo lists", "re-record it", "setaside.resolve", "check ledger", ".whosaidso/cache out of git",
		"git merge --no-edit origin/main", "whosaidso check claim A", "lost claim"} {
		if !strings.Contains(out, want) {
			t.Errorf("help branches must say %q", want)
		}
	}
}

// Flags are accepted after positional ids, and every view answers the same
// either way.
func TestFlagsAfterPositionalIDs(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	for _, pair := range [][2][]string{
		{{"show", "--json", string(cliID(1))}, {"show", string(cliID(1)), "--json"}},
		{{"history", "--json", string(cliID(1))}, {"history", string(cliID(1)), "--json"}},
		{{"continue", "--limit", "1", string(cliID(1))}, {"continue", string(cliID(1)), "--limit", "1"}},
		{{"admit", "--json", "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "checked the task", string(cliID(3))},
			{"admit", string(cliID(3)), "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "checked the task", "--json"}},
	} {
		before, _, code := cliRun(t, root, nil, "", pair[0]...)
		after, errs, code2 := cliRun(t, root, nil, "", pair[1]...)
		if pair[0][0] == "continue" { // continue observes the time; compare its brief's first line only
			before, after = strings.SplitN(before, "\n", 2)[0], strings.SplitN(after, "\n", 2)[0]
		}
		if code != 0 || code2 != 0 || before != after {
			t.Fatalf("%v and %v must answer alike: %d %d %q\n%s\n%s", pair[0], pair[1], code, code2, errs, before, after)
		}
	}
}

// With --json a failure that printed no answer still prints one JSON object.
func TestJSONFailuresPrintAnErrorObject(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	for args, want := range map[string]struct {
		code int
		name string
	}{
		"show --json bad-id": {2, "usage"},
		"admit --json --outcome x --reason r " + string(cliID(3)): {1, "refused"},
		// a flag error stops parsing before or after --json; the request stands
		"capture --json --typo":              {2, "usage"},
		"template task.create --typo --json": {2, "usage"},
		"capture --typo --json=true":         {2, "usage"},
		"capture --typo -json=true":          {2, "usage"},
		"capture ---x --json":                {2, "usage"},
	} {
		out, _, code := cliRun(t, root, nil, "", strings.Fields(args)...)
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if code != want.code || json.Unmarshal([]byte(out), &e) != nil || e.Error.Code != want.name || e.Error.Message == "" {
			t.Fatalf("%s: want exit %d and error code %q, got %d %q", args, want.code, want.name, code, out)
		}
	}
	// --json read as parsing reads it: after -- it is the tool's, as a flag's
	// value it is that value, and a later --json=false turns it off.
	for _, args := range []string{"show bad-id", "run --typo -- tool --json", "capture --reason --json --typo",
		"capture --typo --reason --json", "capture --json --json=false --typo"} {
		if out, _, code := cliRun(t, root, nil, "", strings.Fields(args)...); code != 2 || out != "" {
			t.Fatalf("%s: without --json a failure prints nothing on stdout, got %d %q", args, code, out)
		}
	}
}
