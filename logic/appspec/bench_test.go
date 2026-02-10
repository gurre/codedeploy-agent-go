package appspec

import "testing"

// BenchmarkParse measures appspec YAML parsing throughput.
// This is on the hot path for every deployment: Install and lifecycle hook
// commands all parse the appspec to determine files, hooks, and permissions.
func BenchmarkParse(b *testing.B) {
	data := []byte(`
version: 0.0
os: linux
files:
  - source: /
    destination: /opt/app
hooks:
  BeforeInstall:
    - location: scripts/before.sh
      timeout: 300
  AfterInstall:
    - location: scripts/after.sh
      timeout: 60
permissions:
  - object: /opt/app
    pattern: "**"
    owner: deploy
    group: deploy
    mode: "755"
    type:
      - file
      - directory
`)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPermissionMatchesPattern measures glob matching speed.
// Permission matching runs for every file/directory during Install.
func BenchmarkPermissionMatchesPattern(b *testing.B) {
	p := Permission{Object: "/opt/app", Pattern: "**"}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		p.MatchesPattern("/opt/app/lib/internal/deeply/nested/file.so")
	}
}
