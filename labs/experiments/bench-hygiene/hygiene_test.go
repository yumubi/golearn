package benchhygiene

import "testing"

// TestBothComputeSame 证明两个版本算的东西一致——防折叠不改变语义。
func TestBothComputeSame(t *testing.T) {
	const n = 1000
	want := n * (n - 1) / 2
	if got := Foldable(n); got != want {
		t.Fatalf("foldable: %d != %d", got, want)
	}
	if got := Escaped(n); got != want {
		t.Fatalf("escaped: %d != %d", got, want)
	}
}

func BenchmarkFoldable(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Foldable(10000)
	}
}

func BenchmarkEscaped(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Escaped(10000)
	}
}
