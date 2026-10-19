package main

// Source flags bind original file bytes, speaker and sequence order through
// source.intake's template. Capture owns preservation of the original bytes.

import (
	"flag"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func sourceAddVerb(fs *flag.FlagSet) func(*call) error {
	o := authoringOptions(fs)
	speaker := fs.String("speaker", "", "original speaker `ACTOR` (required)")
	order := fs.Uint64("order", 0, "zero-based source sequence `N` (required; zero is valid)")
	var about blobPaths
	fs.Var(&about, "about", "admitted `TASK` at its current revision (repeatable)")
	return func(c *call) error {
		file := ""
		if len(c.args) == 1 {
			file = c.args[0]
		}
		orderGiven := ""
		if isSet(fs, "order") {
			orderGiven = "given"
		}
		required := append(o.required(), "FILE", file, "--speaker", *speaker, "--order", orderGiven)
		if err := missingFlags(required...); err != nil {
			return err
		}
		if len(c.args) != 1 || c.argv != nil {
			return usageError("source add takes one FILE")
		}
		t, err := authoringTemplate(c, "source.intake", o.actor(c))
		if err != nil {
			return err
		}
		refs, err := t.taskRefs(about)
		if err != nil {
			return err
		}
		if err := t.pinPath("source_ref", file, "", model.Selector{Kind: "whole"}); err != nil {
			return err
		}
		b := templateBinds{sets: blobPaths{jsonSet("speaker", model.Actor{ID: *speaker}), jsonSet("order", *order), jsonSet("referents", refs)}}
		if err := t.fill(b); err != nil {
			return err
		}
		return o.finish(t)
	}
}
