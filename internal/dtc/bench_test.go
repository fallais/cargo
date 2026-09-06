package dtc

import (
	"os"
	"strings"
	"testing"
)

// BenchmarkLoadFullCatalog parses a full-size catalog (~28k codes) to check
// that shipping CSV rather than a prebuilt binary format is affordable at
// startup.
func BenchmarkLoadFullCatalog(b *testing.B) {
	data, err := os.ReadFile(os.Getenv("DTC_BENCH_CSV"))
	if err != nil {
		b.Skip("set DTC_BENCH_CSV to a full catalog")
	}
	s := string(data)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, err := LoadCSV(strings.NewReader(s), "bench")
		if err != nil {
			b.Fatal(err)
		}
		if len(t) < 20000 {
			b.Fatalf("only %d entries", len(t))
		}
	}
}

// BenchmarkIndexedFullCatalog is the same work as BenchmarkLoadFullCatalog but
// building only line offsets, which is what Builtin actually does.
func BenchmarkIndexedFullCatalog(b *testing.B) {
	data, err := os.ReadFile(os.Getenv("DTC_BENCH_CSV"))
	if err != nil {
		b.Skip("set DTC_BENCH_CSV to a full catalog")
	}

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix, err := NewIndexed(data, "bench")
		if err != nil {
			b.Fatal(err)
		}
		if ix.Len() < 20000 {
			b.Fatalf("only %d entries", ix.Len())
		}
	}
}

func BenchmarkIndexedLookup(b *testing.B) {
	data, err := os.ReadFile(os.Getenv("DTC_BENCH_CSV"))
	if err != nil {
		b.Skip("set DTC_BENCH_CSV to a full catalog")
	}
	ix, err := NewIndexed(data, "bench")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Lookup("P0301")
	}
}
