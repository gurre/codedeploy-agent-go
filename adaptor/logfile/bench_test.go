package logfile

import "testing"

// BenchmarkWrite measures the throughput of sequential writes that trigger
// periodic rotation. This benchmarks the hot path: mutex lock, size check,
// occasional file rotation, and the write itself.
func BenchmarkWrite(b *testing.B) {
	dir := b.TempDir()
	w := NewRotatingWriter(dir, "bench.log", 1024*1024, 3)
	if err := w.Open(); err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	data := make([]byte, 128)
	for i := range data {
		data[i] = 'X'
	}

	b.ResetTimer()
	for range b.N {
		_, _ = w.Write(data)
	}
}
