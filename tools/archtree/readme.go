// README splicing: putting the rendered tree between the archtree markers in
// README.md, or reporting that the tree there is stale.
//
// This file edits exactly one region of one file and nothing else. It does not
// render (render.go does) or scan (scan.go does). It replaced
// tools/readme-tree.sh, whose behaviour it keeps: refuse with exit 2 when
// either marker is missing, and with -check write nothing and exit 1 with a
// diff when the README would change.

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	markBegin = "<!-- archtree:begin -->"
	markEnd   = "<!-- archtree:end -->"
)

// syncReadme returns the process exit code.
func syncReadme(root, tree string, check bool) int {
	path := filepath.Join(root, "README.md")
	old, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "archtree:", err)
		return 1
	}
	s := string(old)
	if !strings.Contains(s, markBegin) || !strings.Contains(s, markEnd) {
		fmt.Fprintln(os.Stderr, "archtree: README.md has no archtree markers")
		return 2
	}
	updated := splice(s, tree)
	if check {
		if updated == s {
			return 0
		}
		fmt.Fprintln(os.Stderr, "archtree: the README tree is stale; run: go run ./tools/archtree -readme")
		fmt.Print(lineDiff(s, updated))
		return 1
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "archtree:", err)
		return 1
	}
	fmt.Println("archtree: README.md updated")
	return 0
}

// splice mirrors the awk in the retired shell script line for line: on a
// begin line, print it, then the fenced tree, and skip until an end line.
func splice(readme, tree string) string {
	var b bytes.Buffer
	skip := false
	lines := strings.SplitAfter(readme, "\n")
	for _, l := range lines {
		if l == "" {
			continue
		}
		bare := strings.TrimSuffix(l, "\n")
		if strings.Contains(bare, markBegin) {
			b.WriteString(bare + "\n```text\n" + tree)
			if !strings.HasSuffix(tree, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n")
			skip = true
			continue
		}
		if strings.Contains(bare, markEnd) {
			skip = false
		}
		if !skip {
			// awk always terminates the lines it prints.
			b.WriteString(bare + "\n")
		}
	}
	return b.String()
}

// lineDiff is a minimal stand-in for diff(1): it trims the common head and
// tail and prints what differs between them. Enough to show a reader which
// part of the tree moved, without a dependency.
func lineDiff(a, b string) string {
	x := strings.Split(a, "\n")
	y := strings.Split(b, "\n")
	i := 0
	for i < len(x) && i < len(y) && x[i] == y[i] {
		i++
	}
	j := 0
	for j < len(x)-i && j < len(y)-i && x[len(x)-1-j] == y[len(y)-1-j] {
		j++
	}
	var out strings.Builder
	fmt.Fprintf(&out, "@@ README.md line %d @@\n", i+1)
	for _, l := range x[i : len(x)-j] {
		out.WriteString("< " + l + "\n")
	}
	out.WriteString("---\n")
	for _, l := range y[i : len(y)-j] {
		out.WriteString("> " + l + "\n")
	}
	return out.String()
}
