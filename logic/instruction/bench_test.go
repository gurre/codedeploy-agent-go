package instruction

import (
	"fmt"
	"testing"
)

// BenchmarkBuilderCopyHeavy measures instruction building with many files.
// The builder accumulates commands during Install; large deployments may
// have hundreds of files.
func BenchmarkBuilderCopyHeavy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		builder := NewBuilder()
		for i := range 100 {
			src := fmt.Sprintf("/src/file%d.txt", i)
			dst := fmt.Sprintf("/dst/file%d.txt", i)
			_ = builder.Copy(src, dst)
		}
		builder.Build()
	}
}

// BenchmarkParseRemoveCommands measures cleanup file parsing speed.
// The cleanup file is read before every Install to remove previous deployment files.
func BenchmarkParseRemoveCommands(b *testing.B) {
	var data string
	for i := range 100 {
		data += fmt.Sprintf("/opt/app/file%d.txt\n", i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		ParseRemoveCommands(data)
	}
}
