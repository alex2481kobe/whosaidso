// Scanning: what the instrument can observe about this module.
//
// Everything that READS the module lives here, together with the types it
// fills. Nothing here formats anything for a human, and nothing here decides
// whether what it found is good. Drawing belongs in render.go; judgement
// belongs to a criterion, outside this program entirely.

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	purposeMissing  = "(no package doc comment supplied)"
	purposeTestOnly = "(tests only, no production code)"
)

type pkg struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Purpose is text the author supplied, not a fact this scan measured:
	// the package doc comment's first sentence, or purposeMissing.
	// PurposeFrom names the file it was read from, empty when none was.
	Purpose     string   `json:"purpose"`
	PurposeFrom string   `json:"purpose_from"`
	Files       int      `json:"files"`
	Lines       int      `json:"lines"`
	TestLines   int      `json:"test_lines"`
	Largest     string   `json:"largest_file"`
	LargestN    int      `json:"largest_file_lines"`
	Imports     []string `json:"imports"`
	// FileLines is every production file in the package with its own count,
	// the per-file answer tools/filesize.sh used to give. Test files are
	// excluded here as they were there; their total is TestLines.
	FileLines []fileLines `json:"file_lines"`
}

type fileLines struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}

// report has no report-wide unit. WhoSaidSo's evaluator lets an object's direct
// children inherit its "unit", so one here labelled /cycles and /packages as
// counted in lines: a true word on the wrong question. Every number a criterion
// may select is in Readings and states its own unit.
type report struct {
	Module   string     `json:"module"`
	Packages []pkg      `json:"packages"`
	Cycles   [][]string `json:"cycles"`
	Readings readingSet `json:"readings"`
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
			// The root itself is never skipped, whatever it is called: -root
			// ../.. has a base name starting with a dot, and skipping it
			// reported an empty module rather than this one.
			base := d.Name()
			if path != root && (strings.HasPrefix(base, ".") || base == "testdata") {
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
		// Initialised, not nil, for the reason given in cycles below.
		p := pkg{FileLines: []fileLines{}}
		rel, _ := filepath.Rel(root, dir)
		p.Path = filepath.ToSlash(rel)
		imports := map[string]bool{}
		docs := map[string]string{} // file name -> its package doc comment

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
			p.FileLines = append(p.FileLines, fileLines{Path: filepath.ToSlash(filepath.Join(p.Path, e.Name())), Lines: n})
			if n > p.LargestN {
				p.LargestN, p.Largest = n, e.Name()
			}
			if f.Doc != nil && isPackageDoc(f.Doc.Text(), f.Name.Name) {
				docs[e.Name()] = f.Doc.Text()
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
		if name := packageDocFile(docs); name != "" {
			p.Purpose, p.PurposeFrom = firstSentence(docs[name]), name
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

// isPackageDoc reports whether a comment attached to the package clause is
// the package's doc comment by Go convention: it opens "Package" or, for a
// main package, "Command". The name after it is not checked. Any other attached comment describes
// its file (a command once got "the id implementation" as its purpose that way), and a
// file's comment is never borrowed as the package's purpose.
func isPackageDoc(doc, name string) bool {
	f := strings.Fields(doc)
	if len(f) < 2 {
		return false
	}
	return f[0] == "Package" || name == "main" && f[0] == "Command"
}

// packageDocFile picks which file's package doc to report: doc.go when it has
// one, otherwise the first by name. Empty means none was supplied.
func packageDocFile(docs map[string]string) string {
	if _, ok := docs["doc.go"]; ok {
		return "doc.go"
	}
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
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
