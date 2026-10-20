package main

// Gallery CLI flags translate to capture, admission and ledger projection.
// File pinning and visual acceptance rules live in gallery and reduce.

import (
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

func galleryVerb(mode string) func(*flag.FlagSet) func(*call) error {
	return func(fs *flag.FlagSet) func(*call) error {
		asJSON := new(bool)
		if mode != "import" {
			asJSON = jsonFlag(fs)
		}
		if mode == "list" {
			return func(c *call) error {
				if err := noPositionals(c, "gallery list"); err != nil {
					return err
				}
				p, err := c.project()
				if err != nil {
					return err
				}
				shown, err := gallery.List(p)
				if err != nil {
					return err
				}
				if *asJSON {
					return json.NewEncoder(c.stdout).Encode(map[string]any{"entries": shown})
				}
				if len(shown) == 0 {
					fmt.Fprintln(c.stdout, "the gallery is empty")
				}
				for _, e := range shown {
					by := e.AddedBy
					if by == "" {
						by = "UNKNOWN"
					}
					fmt.Fprintf(c.stdout, "%s  %s  %s  %s  by %s  %s\n  %s\n", e.ID, e.AddedAt.Format(time.RFC3339), e.Status, e.Verdict, by, e.Title, e.Path)
					if e.Note != "" {
						fmt.Fprintln(c.stdout, "  "+e.Note)
					}
				}
				return nil
			}
		}
		actor := actorFlag(fs)
		admit := fs.Bool("admit", false, "then admit under the same actor and self-admission policy")
		reason := fs.String("reason", "", "--admit: the review reason `TEXT`")
		var title, note, by string
		var tasks blobPaths
		if mode == "add" {
			fs.StringVar(&title, "title", "", "a short one-line `TITLE` (required)")
			fs.Var(&tasks, "task", "an admitted task `ID` (repeatable)")
		}
		if mode == "add" || mode == "approve" || mode == "reject" {
			fs.StringVar(&note, "note", "", "words on the entry or verdict; required for reject")
		}
		if mode == "approve" || mode == "reject" {
			fs.StringVar(&by, "by", "", "the identified `ACTOR` recording the verdict (required)")
		}

		return func(c *call) error {
			want := 1
			if mode == "import" {
				want = 0
			}
			if c.argv != nil || len(c.args) != want {
				return usageError("run whosaidso help gallery %s", mode)
			}
			if *admit && model.Blank(*reason) {
				return usageError("gallery --admit needs --reason")
			}
			if !*admit && *reason != "" {
				return usageError("--reason belongs to --admit")
			}
			p, err := c.project()
			if err != nil {
				return err
			}
			author := actor(c)
			if mode == "approve" || mode == "reject" {
				if model.Blank(by) {
					return usageError("gallery %s needs --by ACTOR", mode)
				}
				author = model.Actor{ID: by}
			}
			var events []model.TypedEvent
			switch mode {
			case "add":
				refs, err := gallery.TaskRefs(p, tasks)
				if err != nil {
					return err
				}
				path := c.args[0]
				if !filepath.IsAbs(path) {
					path, err = filepath.Rel(p.Root, filepath.Join(c.cwd, path))
					if err != nil {
						return err
					}
				}
				e, err := gallery.Prepare(p.Root, path, title, note, author, refs, time.Now())
				if err != nil {
					return err
				}
				events = []model.TypedEvent{&e}
			case "rm":
				e, err := gallery.Find(p, c.args[0])
				if err != nil {
					return err
				}
				events = []model.TypedEvent{&model.GalleryRetract{EntryID: model.ID(e.ID)}}
			case "approve", "reject":
				e, err := gallery.Find(p, c.args[0])
				if err != nil {
					return err
				}
				if mode == "approve" {
					pin, err := gallery.Pin(p.Root, e.Locator)
					if err != nil {
						return err
					}
					if !pin.SameContent(model.GalleryFile{SHA256: e.SHA256, Length: e.Length}) {
						return fmt.Errorf("changed: %s; add the current content as a new entry", e.ID)
					}
					events = []model.TypedEvent{&model.GalleryApprove{EntryID: model.ID(e.ID), SHA256: e.SHA256, Actor: author, Note: note}}
				} else {
					events = []model.TypedEvent{&model.GalleryReject{EntryID: model.ID(e.ID), SHA256: e.SHA256, Actor: author, Note: note}}
				}
			case "import":
				result, err := gallery.Import(p, author, time.Now())
				if err != nil {
					return err
				}
				events = result.Events
				for _, skipped := range result.Skipped {
					fmt.Fprintf(c.stderr, "skipped legacy entry %q path %q title %q: %s\n", skipped.LegacyID, skipped.Path, skipped.Title, skipped.Reason)
				}
				for _, merged := range result.Collapsed {
					fmt.Fprintf(c.stderr, "collapsed legacy record %d %q into record %d %q\n", merged.Record, merged.LegacyID, merged.Into, merged.IntoLegacyID)
				}
			}
			for _, event := range events {
				ref, err := gallery.Capture(c.ctx, p, author, event)
				if err != nil {
					return err
				}
				raw, err := model.EncodeEvent(event)
				if err != nil {
					return err
				}
				created := createdRecords([]model.Event{raw}, nil)
				if e, ok := event.(*model.GalleryAdd); ok && !*asJSON {
					fmt.Fprintf(c.stderr, "entry %s\n", e.EntryID)
				}
				if *admit {
					if err := captureAndAdmit(c.ctx, p, c.stdout, *asJSON, ref, 1, created, "", author, *reason); err != nil {
						return err
					}
				} else {
					if err := printResult(c.stdout, *asJSON, captureAnswer{ref.CommandID, ref.Digest, created}, captureAck(ref, 1)); err != nil {
						return err
					}
				}
			}
			return nil
		}
	}
}
