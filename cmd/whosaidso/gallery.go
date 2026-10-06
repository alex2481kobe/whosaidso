package main

// This file holds the gallery verbs: add, list and rm on this checkout's
// project gallery (internal/gallery), in the machine's WhoSaidSo home. The
// gallery is not a record: nothing here reads or writes a ledger or intake,
// and rm never touches the file an entry names. The viewer's Gallery screen
// lives in internal/ui.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// galleryFile is this checkout's project gallery. It needs only the declared
// project id, not a bound home.
func (c *call) galleryFile() (string, error) {
	p, err := c.checkout()
	if err != nil {
		return "", err
	}
	return store.GalleryFile(p.ID)
}

func galleryVerb(mode string) func(*flag.FlagSet) func(*call) error {
	return func(fs *flag.FlagSet) func(*call) error {
		var title, note *string
		var asJSON *bool
		switch mode {
		case "add":
			title = fs.String("title", "", "a short one-line `TITLE` (required)")
			note = fs.String("note", "", "a few words on what to look at")
		case "list":
			asJSON = jsonFlag(fs)
		}
		return func(c *call) error {
			if c.argv != nil || (mode == "list") != (len(c.args) == 0) || len(c.args) > 1 {
				return usageError("whosaidso gallery %s: run whosaidso help gallery %s", mode, mode)
			}
			file, err := c.galleryFile()
			if err != nil {
				return err
			}
			switch mode {
			case "add":
				e, err := gallery.Add(c.ctx, file, c.cwd, c.args[0], *title, *note, c.getenv("WHOSAIDSO_ACTOR"), time.Now())
				if err != nil {
					return err
				}
				fmt.Fprintf(c.stdout, "added %s %s\n", e.ID, e.Path)
			case "rm":
				e, err := gallery.Remove(c.ctx, file, c.args[0])
				if errors.Is(err, gallery.ErrNoEntry) {
					return fmt.Errorf("whosaidso gallery rm: no entry %s; whosaidso gallery list shows the ids", c.args[0])
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(c.stdout, "removed %s from the gallery; %s is untouched\n", e.ID, e.Path)
			case "list":
				return printGallery(c, file, *asJSON)
			}
			return nil
		}
	}
}

func printGallery(c *call, file string, asJSON bool) error {
	shown, err := gallery.List(file)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(c.stdout).Encode(map[string]any{"entries": shown})
	}
	if len(shown) == 0 {
		fmt.Fprintln(c.stdout, "the gallery is empty")
	}
	for _, s := range shown {
		by := s.AddedBy
		if by == "" {
			by = "UNKNOWN"
		}
		fmt.Fprintf(c.stdout, "%s  %s  %s  by %s  %s\n  %s\n", s.ID, s.AddedAt.Local().Format("2006-01-02 15:04"), s.Status, by, s.Title, s.Path)
		if s.Note != "" {
			fmt.Fprintf(c.stdout, "  %s\n", s.Note)
		}
	}
	return nil
}
