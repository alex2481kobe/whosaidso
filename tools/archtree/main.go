// Command archtree is an instrument that reports how this module's packages
// fit together. It emits JSON for a criterion to select from, and it does not
// decide whether the shape it finds is good.
//
// SELECT FROM /readings, and only from there. Each reading there states its
// own unit, population and denominator on the object holding its value or
// values, which is where WhoSaidSo's evaluator looks, and is reached by name:
// /readings/file_lines (every production file, module-wide),
// /readings/by_package/<path with / as ~1>/file_lines, .../largest_file_lines,
// /readings/by_file/<path>, /readings/import_cycles. For a set, the population
// selector is the same pointer plus /values. /packages and /cycles are kept for
// people and the tree; they state no unit, and /packages/N is a position that a
// new package silently re-aims.
//
// VALIDATED, by known-answer tests in known_answer_test.go (each mutation-
// checked): per-file line counts including a final line with no newline;
// package line and test-line totals; the largest production file, which never
// counts a test file and on a tie names the first file by name; testdata/ and
// dot-directories skipped; a test-only package having no largest file; which
// comment supplies a purpose (a package doc, doc.go first, never a file
// comment). NOT validated by any known answer: imports, import cycles.
// criterion_test.go puts the readings through WhoSaidSo's own Observe and Evaluate.
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
//   - complexity. A line is a newline, or a final line without one. Blank
//     lines and comments count like code. A tight 400 line file can be easier
//     to hold in your head than a sprawling 200 line one. Lines are a proxy for
//     the property, not the property.
//   - generated code, which it counts exactly like authored code.
//   - test files, per file. Every per-file reading covers production files
//     only, so a 3000 line test file is visible only inside its package's
//     test_lines total and this instrument will never name it.
//   - Go files under testdata/ or any dot-directory. They are skipped as
//     fixtures, so a real package placed there is not in any reading.
//   - whether a long file is long for a STATED REASON. The house rule permits
//     that, and this instrument cannot read a reason. The threshold lives in a
//     criterion, frozen before the run, never here.
//   - identity across a move. Readings are keyed by path and populations name
//     the module, so a renamed file, package or module is a new key: a frozen
//     criterion then reads nothing (UNKNOWN), never the thing's new self.
//   - which member failed, in WhoSaidSo's words. Set members carry their path, but
//     the evaluator's FALSE reason names a member by index ("value 18"); the
//     index is into /readings/.../values of that run's own output.
//   - test-only packages have no imports reported, because their imports are
//     in _test.go files this does not read. internal/acceptance therefore shows
//     as depending on nothing while in fact it exercises everything.
//   - purpose. It is SUPPLIED, not measured: the first sentence of the package
//     doc comment (attached to the package clause and opening "Package", or
//     "Command" for main), from doc.go when that has one, else the first file
//     by name; purpose_from names the file. With none it says no purpose was
//     supplied, and never borrows a file's own comment, which once made the
//     README describe the whole CLI as the whosaidso id implementation. It reports
//     what the comment SAYS, never whether that is still true of the code.
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

// This command is five files. main.go is the command surface, scan.go reads
// the module, readings.go restates its numbers in the shape a criterion
// selects, render.go draws the tree, readme.go splices it into README.md. Types
// live with the code that fills them, because a type here is a description of
// what was found rather than a shape the renderer needs to know about.

func main() {
	tree := flag.Bool("tree", false, "render the text tree instead of JSON")
	readme := flag.Bool("readme", false, "rewrite the tree in README.md")
	check := flag.Bool("check", false, "exit non-zero if the README tree is stale")
	root := flag.String("root", ".", "module root")
	flag.Parse()

	r, err := measure(*root)
	if err != nil {
		fail(err)
	}
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

// measure is the whole instrument short of printing: what -tree, -readme and
// the JSON all report, and what the known-answer tests run.
func measure(root string) (report, error) {
	mod, err := moduleName(root)
	if err != nil {
		return report{}, err
	}
	pkgs, err := scan(root, mod)
	if err != nil {
		return report{}, err
	}
	cyc := cycles(pkgs)
	return report{Module: mod, Packages: pkgs, Cycles: cyc, Readings: buildReadings(mod, pkgs, cyc)}, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "archtree:", err)
	os.Exit(1)
}
