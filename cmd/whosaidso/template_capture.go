package main

// This file holds what a bound template ends in: printing the event with its
// notes (what was filled, what is left to the author), or --capture, which
// omits optional keys nobody filled, refuses while any placeholder remains,
// and hands the event to capture's own path (captureCLI, captureAndAdmit).
// Filling the template lives in template_bind.go.

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// print writes the event on stdout, and on stderr the notes that still apply
// to it (template_final.go), what was filled and every placeholder still left
// to the author.
func (t *boundTemplate) print() error {
	data, err := renderTemplate(t.event, t.body)
	if err != nil {
		return err
	}
	if _, err := t.c.stdout.Write(data); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(renderTemplateNotes(t.event, t.liveNotes()))
	for _, f := range t.filledLines() {
		fmt.Fprintf(&b, "filled   %s\n", f)
	}
	left := templatePlaceholders(t.body, "")
	if t.bound {
		for _, path := range left {
			fmt.Fprintf(&b, "yours    %s\n", path)
		}
	}
	fmt.Fprintf(&b, "left     %d placeholder(s) for you to fill; --capture refuses while any remains\n", len(left))
	for _, blob := range t.blobs {
		fmt.Fprintf(&b, "blob     capture it with --blob %s\n", blob)
	}
	_, err = io.WriteString(t.c.stderr, b.String())
	return err
}

// capture drops optional keys the author left unfilled, refuses while a placeholder
// remains, then captures (and with --admit admits) through capture's path.
func (t *boundTemplate) capture(admit bool, reason string, jsonOutput bool) error {
	for _, n := range t.notes {
		if n.Kind == "optional" {
			t.body = templateDropUnfilled(t.body, n.Path, "", t.unfilled)
		}
	}
	if left := templatePlaceholders(t.body, ""); len(left) > 0 {
		return fmt.Errorf("template %s: capture refused: %d placeholder(s) unfilled: %s; fill them with --set PATH=VALUE "+
			"(a list you need none of is --set 'PATH=[]' where the rules allow it, quoted since zsh reads [] as a pattern), or print the template and edit it",
			t.event, len(left), strings.Join(left, ", "))
	}
	data, err := renderTemplate(t.event, t.body)
	if err != nil {
		return err
	}
	open := t.c.checkout
	if admit {
		open = t.c.project
	}
	project, err := open()
	if err != nil {
		return err
	}
	ref, events, err := captureCLI(t.c.ctx, project, "", t.author, "-", t.blobs, bytes.NewReader(data))
	if err != nil {
		return err
	}
	count := len(events)
	created := createdRecords(events)
	// The ids this event created, so the next command can name them; with
	// --json they are in the answer instead.
	for _, n := range t.notes {
		if jsonOutput {
			break
		}
		if n.Kind != "minted" || t.putPaths[templatePath(n.Path)] {
			continue // a bind flag or --set replaced the minted id
		}
		steps := make([]templateStep, len(n.Path))
		for i, part := range n.Path {
			steps[i] = templateStep{key: part, index: -1}
			if part == "0" {
				steps[i] = templateStep{index: 0}
			}
		}
		if id, ok := templateGet(t.body, steps); ok {
			fmt.Fprintf(t.c.stderr, "minted   %s = %v\n", templatePath(n.Path), id)
		}
	}
	if admit {
		return captureAndAdmit(t.c.ctx, project, t.c.stdout, jsonOutput, ref, count, created, "", t.author, reason)
	}
	return printResult(t.c.stdout, jsonOutput, captureAnswer{ref.CommandID, ref.Digest, created}, captureAck(ref, count))
}
