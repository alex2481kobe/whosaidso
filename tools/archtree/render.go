// Rendering: turning what the scanner found into the text tree.
//
// This file formats and nothing else. It reads no files, makes no decisions,
// and adds no facts that the JSON does not already carry, so the tree and the
// JSON can never disagree about what was found.

package main

import (
	"fmt"
	"sort"
	"strings"
)

func render(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.Module)

	groups := map[string][]pkg{}
	var order []string
	for _, p := range r.Packages {
		top := p.Path
		if i := strings.Index(p.Path, "/"); i >= 0 {
			top = p.Path[:i]
		}
		if _, seen := groups[top]; !seen {
			order = append(order, top)
		}
		groups[top] = append(groups[top], p)
	}
	sort.Strings(order)

	for gi, top := range order {
		gLast := gi == len(order)-1
		fmt.Fprintf(&b, "%s %s/\n", elbow(gLast), top)
		ps := groups[top]
		for pi, p := range ps {
			pLast := pi == len(ps)-1
			name := strings.TrimPrefix(p.Path, top+"/")
			if name == p.Path {
				name = "."
			}
			fmt.Fprintf(&b, "%s%s %-14s %s\n", indent(gLast), elbow(pLast), name, p.Purpose)

			dep := "leaf"
			if len(p.Imports) > 0 {
				short := make([]string, 0, len(p.Imports))
				for _, im := range p.Imports {
					short = append(short, im[strings.LastIndex(im, "/")+1:])
				}
				dep = "uses " + strings.Join(short, ", ")
			}
			if p.Files == 0 {
				fmt.Fprintf(&b, "%s%s   %d lines of tests\n", indent(gLast), indent(pLast), p.TestLines)
				continue
			}
			fmt.Fprintf(&b, "%s%s   %d files, %d lines (%d test), largest %s at %d -- %s\n",
				indent(gLast), indent(pLast), p.Files, p.Lines, p.TestLines, p.Largest, p.LargestN, dep)
		}
	}
	if len(r.Cycles) > 0 {
		fmt.Fprintf(&b, "\nIMPORT CYCLES: %d\n", len(r.Cycles))
		for _, c := range r.Cycles {
			fmt.Fprintf(&b, "  %s\n", strings.Join(c, " -> "))
		}
	}
	return b.String()
}

func elbow(last bool) string {
	if last {
		return "`--"
	}
	return "|--"
}

func indent(last bool) string {
	if last {
		return "    "
	}
	return "|   "
}
