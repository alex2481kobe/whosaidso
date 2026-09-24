package main

// This file renders help from the usage registry (registry.go) and the
// guide's topics (guide.go): the bare summary, the keyword index, the whole
// guide, one topic, and one verb's usage with the flags its FlagSet really
// declares. Nothing here parses a command or decides what a verb does.

import (
	"flag"
	"fmt"
	"sort"
	"strings"
)

const bareSummary = `WhoSaidSo keeps a project's claims, decisions, instruments and tasks in one
append-only ledger that only admission writes.
The loop: capture proposes, admit judges, run measures, handback reports, a
witness accepts. Reads (todo, continue, show, history) answer from the ledger.
Start with whosaidso todo; everything else: run ` + "`whosaidso help`" + `.
`

// verbHelp is one verb's usage: synopsis, summary, every flag its FlagSet
// declares, and its detail.
func verbHelp(v *verb) string {
	fs := flag.NewFlagSet(v.name, flag.ContinueOnError)
	v.define(fs)
	var b strings.Builder
	synopsis := "whosaidso " + v.name
	var count int
	fs.VisitAll(func(*flag.Flag) { count++ })
	if count > 0 {
		synopsis += " [flags]"
	}
	if v.args != "" {
		synopsis += " " + v.args
	}
	fmt.Fprintf(&b, "%s\n  %s\n", synopsis, v.summary)
	if count > 0 {
		b.WriteString("flags:\n")
	}
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		head := "--" + f.Name
		if name != "" {
			head += " " + name
		}
		if d := f.DefValue; d != "" && d != "0" && d != "false" && d != "0s" {
			usage += fmt.Sprintf(" (default %q)", d)
		}
		fmt.Fprintf(&b, "  %-26s %s\n", head, usage)
	})
	b.WriteString(v.detail)
	return b.String()
}

// groupHelp is every mode of a verb group, such as check.
func groupHelp(group string) string {
	var parts []string
	for _, mode := range groupModes(group) {
		v, _ := lookup([]string{group, mode})
		parts = append(parts, verbHelp(v))
	}
	return strings.Join(parts, "\n")
}

// index is the keyword index: topics, then verbs, alphabetical within each.
func index() string {
	var b strings.Builder
	b.WriteString("TOPICS\n")
	topics := append([]topic{}, guide...)
	sort.Slice(topics, func(i, j int) bool { return topics[i].name < topics[j].name })
	for _, t := range topics {
		fmt.Fprintf(&b, "  %-16s %-54s whosaidso help %s\n", t.name, t.summary, t.name)
	}
	b.WriteString("VERBS\n")
	verbs := append([]verb{}, registry...)
	sort.Slice(verbs, func(i, j int) bool { return verbs[i].name < verbs[j].name })
	for _, v := range verbs {
		fmt.Fprintf(&b, "  %-16s %-54s whosaidso help %s\n", v.name, v.summary, v.name)
	}
	return b.String()
}

// wholeGuide is the index, every topic in reading order, then every verb.
func wholeGuide() string {
	parts := []string{index()}
	for _, t := range guide {
		parts = append(parts, topicHelp(t))
	}
	parts = append(parts, "VERB REFERENCE\n")
	for i := range registry {
		parts = append(parts, verbHelp(&registry[i]))
	}
	return strings.Join(parts, "\n")
}

func topicHelp(t topic) string {
	return strings.ToUpper(t.name) + ": " + t.summary + "\n\n" + t.text
}

// helpFor is `whosaidso help NAME`: the whole guide for no name; a topic, a
// verb, or both when a topic shares a verb's name; every mode of a group.
func helpFor(name string) (string, bool) {
	if name == "" {
		return wholeGuide(), true
	}
	var parts []string
	for _, t := range guide {
		if t.name == name {
			parts = append(parts, topicHelp(t))
		}
	}
	if v, rest := lookup(strings.Fields(name)); v != nil && len(rest) == 0 {
		parts = append(parts, verbHelp(v))
	} else if len(groupModes(name)) > 0 {
		parts = append(parts, groupHelp(name))
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}
