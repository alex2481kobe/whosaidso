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
//   - complexity. Lines are a proxy for how much is in a file, not a measure of
//     how hard it is to hold in your head.
//   - generated code, which it counts exactly like authored code.
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
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The two purposes the scanner supplies itself, rather than reading.
const (
	purposeMissing  = "(no package comment)"
	purposeTestOnly = "(tests only, no production code)"
)

type pkg struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	Purpose   string   `json:"purpose"`
	Files     int      `json:"files"`
	Lines     int      `json:"lines"`
	TestLines int      `json:"test_lines"`
	Largest   string   `json:"largest_file"`
	LargestN  int      `json:"largest_file_lines"`
	Imports   []string `json:"imports"`
}

type report struct {
	Module   string     `json:"module"`
	Unit     string     `json:"unit"`
	Packages []pkg      `json:"packages"`
	Cycles   [][]string `json:"cycles"`
}

func main() {
	tree := flag.Bool("tree", false, "render the text tree instead of JSON")
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

func moduleName(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module line in go.mod")
}

func scan(root, mod string) ([]pkg, error) {
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base != "." && (strings.HasPrefix(base, ".") || base == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var out []pkg
	fset := token.NewFileSet()
	for dir := range dirs {
		p := pkg{}
		rel, _ := filepath.Rel(root, dir)
		p.Path = filepath.ToSlash(rel)
		imports := map[string]bool{}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			f, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			n := countLines(full)
			if strings.HasSuffix(e.Name(), "_test.go") {
				p.TestLines += n
				continue
			}
			p.Name = f.Name.Name
			p.Files++
			p.Lines += n
			if n > p.LargestN {
				p.LargestN, p.Largest = n, e.Name()
			}
			if f.Doc != nil && p.Purpose == "" {
				p.Purpose = firstSentence(f.Doc.Text())
			}
			for _, im := range f.Imports {
				path := strings.Trim(im.Path.Value, `"`)
				if inner, ok := strings.CutPrefix(path, mod+"/"); ok {
					imports[inner] = true
				}
			}
		}
		// A package with no production file is still a package. Skipping
		// them would have hidden internal/acceptance, which holds the
		// independent verification this project leans on hardest: the one
		// package whose absence from the picture would matter most.
		if p.Files == 0 && p.TestLines == 0 {
			continue
		}
		if p.Files == 0 {
			p.Purpose = purposeTestOnly
		}
		if p.Files > 0 && p.Purpose == "" {
			// Reported rather than left blank. A blank column reads as
			// "nothing to say"; this says the comment is missing.
			p.Purpose = purposeMissing
		}
		for k := range imports {
			p.Imports = append(p.Imports, k)
		}
		sort.Strings(p.Imports)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		n++
	}
	return n
}

// firstSentence takes the package comment's opening sentence, with the Go
// convention "Package x ..." trimmed off the front.
func firstSentence(doc string) string {
	doc = strings.TrimSpace(strings.ReplaceAll(doc, "\n", " "))
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i]
	}
	doc = strings.TrimSuffix(doc, ".")
	w := firstWord(doc)
	for _, lead := range []string{"Package ", "Command "} {
		for _, verb := range []string{" is the ", " is an ", " is a ", " is ", " "} {
			if rest, ok := strings.CutPrefix(doc, lead+w+verb); ok {
				return strings.Join(strings.Fields(rest), " ")
			}
		}
	}
	return strings.Join(strings.Fields(doc), " ")
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

// cycles reports import cycles. Go forbids them, so this should always be
// empty; it is here because an instrument that can only report good news is
// not an instrument.
func cycles(pkgs []pkg) [][]string {
	graph := map[string][]string{}
	for _, p := range pkgs {
		graph[p.Path] = p.Imports
	}
	// Initialised, not nil. A nil slice marshals to JSON null, and a criterion
	// selecting the length of null gets an error where the honest answer is
	// zero. Reporting "no cycles" as "no answer" is the failure this whole
	// repository is about.
	found := [][]string{}
	var path []string
	state := map[string]int{}
	var visit func(string)
	visit = func(n string) {
		state[n] = 1
		path = append(path, n)
		for _, m := range graph[n] {
			switch state[m] {
			case 0:
				visit(m)
			case 1:
				for i, x := range path {
					if x == m {
						found = append(found, append([]string(nil), path[i:]...))
						break
					}
				}
			}
		}
		path = path[:len(path)-1]
		state[n] = 2
	}
	for _, p := range pkgs {
		if state[p.Path] == 0 {
			visit(p.Path)
		}
	}
	return found
}

// render draws the tree. Packages are grouped by their top directory, and each
// one shows what it is for, how big it is, and what inside this module it
// depends on. "leaf" means it imports nothing from this module, which is the
// property that makes a package testable on its own.
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
