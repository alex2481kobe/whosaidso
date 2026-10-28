// Selected bundle reads preserve the hashes of a validated prefix without
// decoding unrelated bundles. Snapshot selection and full replay live elsewhere.
package store

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func (s State) selectedBundle(i int) (model.Bundle, error) {
	data, err := readBundle(filepath.Join(s.project.Ledger, s.files[i].name))
	if err != nil {
		return model.Bundle{}, storeFault("io", filepath.Join(s.project.Ledger, s.files[i].name), err.Error())
	}
	if sha256.Sum256(data) != s.files[i].hash {
		return model.Bundle{}, storeFault("ledger-corrupt", filepath.Join(s.project.Ledger, s.files[i].name), "the bundle changed after this read selected it")
	}
	return decodeSelected(s.project, s.files, i, data)
}

// BundleRange reads at most count bundles by canonical position, starting at
// offset. A changed selected file is refused, exactly as in Bundles.
func (s State) BundleRange(offset, count int) ([]model.Bundle, error) {
	if offset < 0 || count < 0 {
		return nil, fmt.Errorf("bundle range must be nonnegative")
	}
	length := len(s.files)
	if s.bundles != nil {
		length = len(s.bundles)
	}
	offset = min(offset, length)
	end := offset + min(count, length-offset)
	if s.bundles != nil {
		return s.bundles[offset:end], nil
	}
	out := make([]model.Bundle, 0, end-offset)
	for i := offset; i < end; i++ {
		b, err := s.selectedBundle(i)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// BundlesByRef reads the named bundles, in canonical order, from the selected
// prefix. A ref the prefix does not hold is an error, never skipped, and a
// changed selected file is refused, exactly as in Bundles.
func (s State) BundlesByRef(refs []model.BundleRef) ([]model.Bundle, error) {
	sorted := append([]model.BundleRef(nil), refs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	length := len(s.files)
	at := func(i int) model.BundleRef { return s.files[i].ref() }
	if s.bundles != nil {
		length = len(s.bundles)
		at = func(i int) model.BundleRef { return s.bundles[i].Ref() }
	}
	out := make([]model.Bundle, 0, len(sorted))
	for _, ref := range sorted {
		i := sort.Search(length, func(i int) bool { return !at(i).Before(ref) })
		if i == length || at(i) != ref {
			return nil, fmt.Errorf("bundle %d %s is not in the selected prefix", ref.Sequence, ref.CommandID)
		}
		if s.bundles != nil {
			out = append(out, s.bundles[i])
			continue
		}
		b, err := s.selectedBundle(i)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
