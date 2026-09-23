package main

// This file holds `datum template --pin NAME=PATH[@REV][#POINTER]`: an
// artifact reference built from the real thing. PATH alone is a content pin
// of the bytes on disk now (digest, length, a media type the bytes satisfy);
// PATH@REV is a git pin of the committed object (full commit, object format).
// The selector is exactly what was given: #POINTER selects a JSON pointer,
// none selects the whole artifact; no unit or meaning is added. Every pin is
// resolved back through the evidence resolver admission uses before it is
// put in the template. Where the pin goes lives in template_tree.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
)

// pin builds one --pin and puts it at NAME.
func (t *boundTemplate) pin(spec string) error {
	name, target, ok := strings.Cut(spec, "=")
	if !ok || name == "" || target == "" {
		return usageError("datum template: --pin takes NAME=PATH[@REV][#POINTER], got %q", spec)
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
	if _, example := t.examples[path]; example && rev != "" {
		return usageError("datum template: --pin %s: %s is an example output, which has no commit", name, path)
	}
	if rev != "" {
		if ref, err = gitPin(t, root, path, rev); err != nil {
			return err
		}
		from = fmt.Sprintf("git %s at %s", path, ref.Git.Commit)
	} else {
		file, example := t.examples[path]
		if !example {
			file = filepath.Join(root, filepath.FromSlash(path))
		}
		if data, err = os.ReadFile(file); err != nil {
			return fmt.Errorf("template: --pin %s: %w", name, err)
		}
		ref = model.ArtifactRef{Kind: "content", Selector: model.Selector{Kind: "whole"},
			Content: &model.ContentPin{SHA256: model.HashBytes(data), Length: uint64(len(data)), MediaType: mediaTypeOf(data), Locators: []model.Locator{{Path: path}}}}
		from = fmt.Sprintf("content of %s: %d bytes, %s", file, len(data), ref.Content.MediaType)
		if example {
			from = fmt.Sprintf("example %s for the output %s: %d bytes, %s; an example, never an observation", file, path, len(data), ref.Content.MediaType)
		}
		t.blobs = append(t.blobs, file)
	}
	if err := model.ValidateArtifactRef(ref, name); err != nil {
		return fmt.Errorf("template: --pin %s: %w", name, err)
	}
	// The pin is checked against the real thing before anyone reads it: git
	// and on-disk content through the resolver admission uses. An example's
	// locator names a run output that exists only in a run's directory, so
	// its identity is the bytes just read, which travel with the capture.
	if _, example := t.examples[path]; !example || rev != "" {
		resolved, err := evidence.NewResolverAt(root, project.ArtifactDir()).Resolve(t.c.ctx, ref)
		if err != nil {
			return fmt.Errorf("template: --pin %s does not resolve: %w", name, err)
		}
		if data != nil && (resolved.SHA256 != model.HashBytes(data) || resolved.Length != uint64(len(data))) {
			return fmt.Errorf("template: --pin %s: the bytes changed while being pinned", name)
		}
	}
	ref.Selector = selector
	if err := model.ValidateArtifactRef(ref, name); err != nil {
		return fmt.Errorf("template: --pin %s: %w", name, err)
	}
	return t.put(name, ref, from+", selector "+selectorText(selector))
}

// gitPin is PATH as committed at REV: the full commit and object format.
func gitPin(t *boundTemplate, root, path, rev string) (model.ArtifactRef, error) {
	format, err := evidence.ExecGit(t.c.ctx, root, "rev-parse", "--show-object-format")
	if err != nil {
		return model.ArtifactRef{}, fmt.Errorf("template: --pin %s@%s: not a readable git checkout: %w", path, rev, err)
	}
	commit, err := evidence.ExecGit(t.c.ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return model.ArtifactRef{}, fmt.Errorf("template: --pin %s@%s: %s is not a commit here", path, rev, rev)
	}
	return model.ArtifactRef{Kind: "git", Selector: model.Selector{Kind: "whole"},
		Git: &model.GitPin{ObjectFormat: strings.TrimSpace(string(format)), Commit: strings.TrimSpace(string(commit)), Path: path}}, nil
}

// refSlot refuses a NAME that is not an artifact reference in this event, so
// a pin cannot land in a text field.
func (t *boundTemplate) refSlot(name string) error {
	steps, err := parseTemplatePath(name)
	if err != nil {
		return usageError("datum template: %v", err)
	}
	node, ok := templateGet(t.body, steps)
	if !ok && len(steps) > 1 && steps[len(steps)-1].index > 0 {
		// one past the end appends: the element must look like its siblings
		node, ok = templateGet(t.body, append(append([]templateStep(nil), steps[:len(steps)-1]...), templateStep{index: 0}))
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
	return usageError("datum template %s: --pin %s: that is not an artifact reference here", t.event, name)
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
