// Command archtree is an instrument that reports how this module's packages
// fit together. It emits JSON for a criterion to select from, and it does not
// decide whether the shape it finds is good.
//
// BLIND TO, and this matters more than the numbers it prints:
//
//   - USE. It reads import statements. A package that imports another may call
//     one function from it or half of it, and this cannot tell the difference.
//     An import is evidence of a dependency, not of its weight.
//   - runtime coupling. Two packages joined only through an interface, a
//     callback or reflection have no import between them and appear unrelated
//     here. Some of the most important seams in a codebase are invisible to it.
//   - build tags. Every file is parsed regardless of GOOS, so the four
//     OS-specific files in internal/store all appear at once even though no
//     single build ever compiles more than two of them.
//   - complexity. A tight 400 line file can be easier to hold in your head
//     than a sprawling 200 line one. Lines are a proxy for the property, not
//     the property.
//   - generated code, which it counts exactly like authored code.
//   - test files, per file. file_lines lists production files only, so a 3000
//     line test file is visible only inside its package's test_lines total and
//     this instrument will never name it.
//   - whether a long file is long for a STATED REASON. The house rule permits
//     that, and this instrument cannot read a reason. The threshold lives in a
//     criterion, frozen before the run, never here.
//   - test-only packages have no imports reported, because their imports are
//     in _test.go files this does not read. internal/acceptance therefore shows
//     as depending on nothing while in fact it exercises everything.
//   - purpose. The one-line purpose is the first sentence of the package doc
//     comment. It reports what the comment SAYS, never whether that is still
//     true of the code.
//
// Usage:
//
//	go run ./tools/archtree            JSON to stdout
//	go run ./tools/archtree -tree      the text tree
//	go run ./tools/archtree -readme    rewrite the tree between the README markers
//	go run ./tools/archtree -check     write nothing; exit 1 with a diff if the README tree is stale
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// This command is four files. main.go is the command surface, scan.go reads
// the module, render.go draws the tree, readme.go splices it into README.md. Types live with the code that fills
// them, in scan.go, because a type here is a description of what the scanner
// found rather than a shape the renderer needs to know about.

func main() {
	tree := flag.Bool("tree", false, "render the text tree instead of JSON")
	readme := flag.Bool("readme", false, "rewrite the tree in README.md")
	check := flag.Bool("check", false, "exit non-zero if the README tree is stale")
	root := flag.String("root", ".", "module root")
	flag.Parse()

	mod, err := moduleName(*root)
	if err != nil {
		fail(err)
	}
	pkgs, err := scan(*root, mod)
	if err != nil {
		fail(err)
	}
	r := report{Module: mod, Unit: "lines", Packages: pkgs, Cycles: cycles(pkgs)}
	if *readme || *check {
		os.Exit(syncReadme(*root, render(r), *check))
	}
	if *tree {
		fmt.Print(render(r))
		return
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "archtree:", err)
	os.Exit(1)
}
