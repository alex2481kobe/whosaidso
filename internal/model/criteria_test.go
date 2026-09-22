package model

// Decimal resource boundaries and their measured conversion cost live here.

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func TestDecimalRatExponentBounds(t *testing.T) {
	for _, s := range []string{"1e4097", "1e-4097", "1E+4097", "1E-4097", "1e1000000", "1e-1000000", "1e999999999999999999999999", "0e4097"} {
		t.Run(s, func(t *testing.T) {
			r, err := DecimalRat(json.Number(s))
			f, ok := err.(*Fault)
			if r != nil || !ok || f.Code != "invalid-field" || f.Path != "number" || !strings.Contains(f.Detail, "[-4096, 4096]") {
				t.Fatalf("must refuse unsupported exponent with bounds: nonnil rational=%t, %v", r != nil, err)
			}
			if _, err := CompareScalars(schemaNumber("1"), Less, schemaNumber(s)); err == nil {
				t.Fatal("comparison accepted unsupported exponent")
			}
			if err := ValidateSchema(schemaNumber(s)); err == nil {
				t.Fatal("schema accepted unsupported exponent")
			}
		})
	}
	for _, s := range []string{"1e4096", "1e-4096", "1E+4096", "-1E-4096", "1e00004096", "0e-4096", "1.25e2", "9007199254740993"} {
		t.Run(s, func(t *testing.T) {
			got, err := DecimalRat(json.Number(s))
			want, ok := new(big.Rat).SetString(s)
			if err != nil || !ok || got.Cmp(want) != 0 {
				t.Fatalf("supported decimal must remain exact: %v, %v", got, err)
			}
		})
	}
}

var decimalBenchmarkResult *big.Rat
var decimalBenchmarkError error

func BenchmarkDecimalRatExponent(b *testing.B) {
	for _, s := range []string{"1.25", "1e4096", "1e-4096", "1e1000000", "1e-1000000"} {
		b.Run(s, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decimalBenchmarkResult, decimalBenchmarkError = DecimalRat(json.Number(s))
			}
		})
	}
}
