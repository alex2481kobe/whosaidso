// Readings: the scanner's numbers restated in the shape a WhoSaidSo criterion can
// select.
//
// What belongs here is the mapping from what scan.go found to readings keyed by
// a stable name (a package path, a file path, a fixed reading name), each one
// stating its own value, unit, population and denominator on the object that
// holds the value. Nothing here reads the module and nothing here judges a
// number: counting is scan.go's job, and the threshold belongs to a criterion.
//
// Why the shape is exactly this: WhoSaidSo's evaluator (internal/evidence, Select
// and metaFrom) takes a reading's unit, population and denominator from the
// selected object or its immediate parent, never from further up, and it reads
// a number from that object's "value" or a set from its "values". A number
// whose unit sits anywhere else is UNKNOWN. And a JSON pointer through an array
// index names a position, so a new package sorting in ahead would silently
// re-aim a frozen criterion; every pointer into this object is by name.

package main

import "path"

const (
	unitLines  = "lines"
	unitCycles = "cycles"
	denomFiles = "production Go files"
	denomPkgs  = "packages"
	denomGraph = "import graphs"
)

// scalar is one selectable number. Path names the member it describes when its
// key does not, as for a package's largest file.
type scalar struct {
	Unit        string `json:"unit"`
	Population  string `json:"population"`
	Denominator string `json:"denominator"`
	Path        string `json:"path,omitempty"`
	Value       int    `json:"value"`
}

// set is one selectable collection. A criterion selects it whole for its
// result, and its "values" member as the population: the evaluator then finds
// the unit, population and denominator on this object, the immediate parent.
type set struct {
	Unit        string        `json:"unit"`
	Population  string        `json:"population"`
	Denominator string        `json:"denominator"`
	Values      []namedNumber `json:"values"`
}

// namedNumber is one member of a set. The path is its name; the evaluator reads
// only value, and the path is there so a person can see which member failed.
type namedNumber struct {
	Path  string `json:"path"`
	Value int    `json:"value"`
}

type packageReadings struct {
	FileLines set    `json:"file_lines"`
	Lines     scalar `json:"lines"`
	TestLines scalar `json:"test_lines"`
	// Largest is absent for a package with no production file: there is no
	// largest file, and a zero would read as a real, very small one.
	Largest *scalar `json:"largest_file_lines,omitempty"`
}

type readingSet struct {
	FileLines    set                        `json:"file_lines"`
	PackageLines set                        `json:"package_lines"`
	ImportCycles scalar                     `json:"import_cycles"`
	ByPackage    map[string]packageReadings `json:"by_package"`
	ByFile       map[string]scalar          `json:"by_file"`
}

// newSet never leaves Values nil, so an empty population marshals as [] and a
// criterion sees an empty set rather than no reading at all.
func newSet(unit, population, denominator string, values []namedNumber) set {
	if values == nil {
		values = []namedNumber{}
	}
	return set{Unit: unit, Population: population, Denominator: denominator, Values: values}
}

func buildReadings(mod string, pkgs []pkg, cycles [][]string) readingSet {
	allFiles := "production Go files in module " + mod
	allPkgs := "packages in module " + mod
	out := readingSet{
		ImportCycles: scalar{Unit: unitCycles, Population: "import graph of module " + mod,
			Denominator: denomGraph, Value: len(cycles)},
		ByPackage: map[string]packageReadings{},
		ByFile:    map[string]scalar{},
	}
	var files, lines []namedNumber
	for _, p := range pkgs {
		pkgPop := "package " + path.Join(mod, p.Path)
		var own []namedNumber
		for _, f := range p.FileLines {
			own = append(own, namedNumber{Path: f.Path, Value: f.Lines})
			out.ByFile[f.Path] = scalar{Unit: unitLines, Population: "production Go file " + f.Path,
				Denominator: denomFiles, Value: f.Lines}
		}
		files = append(files, own...)
		lines = append(lines, namedNumber{Path: p.Path, Value: p.Lines})
		pr := packageReadings{
			FileLines: newSet(unitLines, "production Go files in package "+path.Join(mod, p.Path), denomFiles, own),
			Lines:     scalar{Unit: unitLines, Population: pkgPop, Denominator: denomPkgs, Value: p.Lines},
			TestLines: scalar{Unit: unitLines, Population: "tests of " + pkgPop, Denominator: denomPkgs, Value: p.TestLines},
		}
		if p.Files > 0 {
			pr.Largest = &scalar{Unit: unitLines, Population: pkgPop, Denominator: denomPkgs,
				Path: path.Join(p.Path, p.Largest), Value: p.LargestN}
		}
		out.ByPackage[p.Path] = pr
	}
	out.FileLines = newSet(unitLines, allFiles, denomFiles, files)
	out.PackageLines = newSet(unitLines, allPkgs, denomPkgs, lines)
	return out
}
