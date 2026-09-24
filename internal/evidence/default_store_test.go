package evidence

// This file guards one rule of R13.1 at the source level: production outside
// this package never reaches the default artifact store. Resolution tests
// belong in resolve_test.go, run-directory tests in run_dir_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project's artifact store is derived from its configured ledger
// (store.Project.ArtifactDir). The root-only constructors and the default
// directory exist for callers that know no project; a production caller using
// them would put blobs or run directories at a second, hard-coded location.
func TestProductionNeverUsesTheDefaultArtifactStore(t *testing.T) {
	repo := filepath.Join("..", "..")
	var scanned int
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != repo {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, use := range []string{"evidence.NewResolver(", "evidence.RunDir(", "evidence.DefaultArtifactDir", "evidence.RecordDir"} {
			if strings.Contains(string(data), use) {
				t.Errorf("%s uses %s; production must use the project's ledger-derived store", path, use)
			}
		}
		// RecordDir is the one spelling of the folder name in code. The quote
		// is part of the check: ".whosaidso" as a Go string literal, not the
		// ~/.whosaidso machine directory, which store names as a path part.
		if !strings.HasPrefix(filepath.ToSlash(path), "../../internal/evidence/") && strings.Contains(string(data), "\".whosaidso/") {
			t.Errorf("%s spells the record folder; derive it from the project's ledger", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 20 {
		t.Fatalf("scanned only %d production files; the walk did not reach the repository", scanned)
	}
}
