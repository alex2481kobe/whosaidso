package main

// This file holds `whosaidso template --pin NAME=PATH[@REV][#POINTER]`: an
// artifact reference built from the real thing. PATH alone is a content pin
// of the bytes on disk now (digest, length, a media type the bytes satisfy);
// PATH@REV is a git pin of the committed object (full commit, object format).
// A PATH outside the project is pinned by content alone, with no locator:
// its bytes travel with the capture as a blob. The selector is exactly what
// was given: #POINTER selects a JSON pointer, none selects the whole
// artifact. Every pin in the project is resolved back through the evidence
// resolver admission uses before it is put in the template. Where the pin
// goes lives in template_tree.go; what else its bytes fill, once the draft is final, in
// template_derive.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// pin builds one --pin and puts it at NAME.
func (t *boundTemplate) pin(spec string) error {
	name, target, ok := strings.Cut(spec, "=")
	if !ok || name == "" || target == "" {
		return usageError("whosaidso template: --pin takes NAME=PATH[@REV][#POINTER], got %q", spec)
	}
	if err := t.refSlot(name); err != nil {
		return err
	}
	target, pointer, hasPointer := strings.Cut(target, "#")
	selector := model.Selector{Kind: "whole"}
	if hasPointer {
		selector = model.Selector{Kind: "json-pointer", Pointer: pointer}
	}
	path, rev := target, ""
	if i := strings.LastIndex(target, "@"); i >= 0 {
		path, rev = target[:i], target[i+1:]
	}
	project, err := store.Discover(t.c.cwd)
	if err != nil {
		return err
	}
	root := project.ExecRoot()
	var ref model.ArtifactRef
	var from string
	var data []byte
	_, example := t.examples[path]
	// A PATH that is not lexically inside the project has no project-relative
	// locator to store; its identity is the bytes read now, as an example's is.
	outside := !example && rev == "" && !filepath.IsLocal(filepath.FromSlash(path))
	if example && rev != "" {
		return usageError("whosaidso template: --pin %s: %s is an example output, which has no commit", name, path)
	}
	if rev != "" {
		if ref, err = gitPin(t, root, path, rev); err != nil {
			return err
		}
		from = fmt.Sprintf("git %s at %s", path, ref.Git.Commit)
	} else {
		file, locators := t.examples[path], []model.Locator{{Path: path}}
		switch {
		case outside && filepath.IsAbs(path):
			file, locators = path, []model.Locator{}
		case outside:
			file, locators = filepath.Join(root, filepath.FromSlash(path)), []model.Locator{}
		case !example:
			file = filepath.Join(root, filepath.FromSlash(path))
		}
		if data, err = os.ReadFile(file); err != nil {
			return fmt.Errorf("template: --pin %s: %w", name, err)
		}
		ref = model.ArtifactRef{Kind: "content", Selector: model.Selector{Kind: "whole"},
			Content: &model.ContentPin{SHA256: model.HashBytes(data), Length: uint64(len(data)), MediaType: mediaTypeOf(data), Locators: locators}}
		from = fmt.Sprintf("content of %s: %d bytes, %s", file, len(data), ref.Content.MediaType)
		if example {
			from = fmt.Sprintf("example %s for the output %s: %d bytes, %s; an example, never an observation", file, path, len(data), ref.Content.MediaType)
		}
		if outside {
			from += "; outside the project, so no locator: the bytes travel as a blob"
		}
		if !slices.Contains(t.blobs, file) {
			t.blobs = append(t.blobs, file) // one blob however many pins read it
		}
	}
	if err := model.ValidateArtifactRef(ref, name); err != nil {
		return fmt.Errorf("template: --pin %s: %w", name, err)
	}
	pinned := evidence.ResolvedArtifact{Ref: ref, Bytes: data, SHA256: model.HashBytes(data), Length: uint64(len(data))}
	// The pin is checked against the real thing before anyone reads it: git
	// and on-disk content through the resolver admission uses. An example's
	// locator is the name of a run output inside a run, not a path, and a
	// file outside the project has no locator, so their identity is the bytes
	// just read, which travel with the capture.
	if (!example && !outside) || rev != "" {
		resolved, err := evidence.NewResolverAt(root, project.ArtifactDir()).Resolve(t.c.ctx, ref)
		if err != nil {
			return fmt.Errorf("template: --pin %s does not resolve: %w", name, err)
		}
		if data != nil && (resolved.SHA256 != model.HashBytes(data) || resolved.Length != uint64(len(data))) {
			return fmt.Errorf("template: --pin %s: the bytes changed while being pinned", name)
		}
		pinned = resolved
	}
	ref.Selector = selector
	if err := model.ValidateArtifactRef(ref, name); err != nil {
		return fmt.Errorf("template: --pin %s: %w", name, err)
	}
	if err := t.put(name, ref, from+", selector "+selectorText(selector)); err != nil {
		return err
	}
	t.pinned = append(t.pinned, pinned) // what its bytes state is read from the final draft
	return nil
}

// gitPin is PATH as committed at REV: the full commit and object format.
func gitPin(t *boundTemplate, root, path, rev string) (model.ArtifactRef, error) {
	format, err := objectFormat(t.c.ctx, root)
	if err != nil {
		return model.ArtifactRef{}, fmt.Errorf("template: --pin %s@%s: not a readable git checkout: %w", path, rev, err)
	}
	commit, err := evidence.ExecGit(t.c.ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return model.ArtifactRef{}, fmt.Errorf("template: --pin %s@%s: %s is not a commit here", path, rev, rev)
	}
	return model.ArtifactRef{Kind: "git", Selector: model.Selector{Kind: "whole"},
		Git: &model.GitPin{ObjectFormat: format, Commit: strings.TrimSpace(string(commit)), Path: path}}, nil
}

// objectFormat is the object format git reports for the repository at root.
func objectFormat(ctx context.Context, root string) (string, error) {
	format, err := evidence.ExecGit(ctx, root, "rev-parse", "--show-object-format")
	return strings.TrimSpace(string(format)), err
}

// refSlot refuses a NAME that is not an artifact reference in this event, so
// a pin cannot land in a text field. A NAME the tree lacks is judged by the
// schema's skeleton there (a list element past the end, an omitted optional
// key); whether it may grow there is put's (template_grow.go).
func (t *boundTemplate) refSlot(name string) error {
	steps, err := parseTemplatePath(name)
	if err != nil {
		return usageError("whosaidso template: %v", err)
	}
	node, ok := templateGet(t.body, steps)
	if !ok {
		node, ok = t.skeletonAt(steps)
	}
	object, isObject := node.(templateObject)
	if ok && isObject {
		keys := map[string]bool{}
		for _, m := range object {
			keys[m.key] = true
		}
		if keys["kind"] && keys["selector"] {
			return nil
		}
	}
	return usageError("whosaidso template %s: --pin %s: that is not an artifact reference here", t.event, name)
}

// mediaTypeOf names the narrowest media type whose byte-level check (the one
// admission applies) these bytes pass; it reads no file name.
func mediaTypeOf(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case utf8.Valid(b) && json.Valid(b):
		return "application/json"
	case utf8.Valid(b) && !bytes.ContainsFunc(b, func(r rune) bool {
		return r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f' || r == 0x7f
	}):
		return "text/plain"
	}
	return "application/octet-stream"
}

func selectorText(s model.Selector) string {
	if s.Kind == "json-pointer" {
		return "json-pointer " + s.Pointer
	}
	return s.Kind
}
