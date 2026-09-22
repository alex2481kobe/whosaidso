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
			// Deliberately no line counts. The first version printed
			// production and test lines and the largest file, and the CI
			// staleness check then failed on almost every commit, because a
			// single added test line changes the rendered tree. A check that
			// fails constantly is noise, and noise gets ignored, which is
			// worse than no check at all. I tripped it twice within an hour
			// of adding it.
			//
			// The counts still exist, in the JSON, where a criterion can
			// select them and where churn costs nothing. Size is what
			// tools/filesize.sh is for. This tree answers how the packages
			// fit together, and that changes only when the structure does.
			//
			// Whether a package is tested at all IS structure, so that stays.
			tested := "no tests"
			if p.TestLines > 0 {
				tested = "tested"
			}
			if p.Files == 0 {
				fmt.Fprintf(&b, "%s%s   tests only\n", indent(gLast), indent(pLast))
				continue
			}
			fmt.Fprintf(&b, "%s%s   %d files, %s -- %s\n",
				indent(gLast), indent(pLast), p.Files, tested, dep)
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
