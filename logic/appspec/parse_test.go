package appspec

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testOS returns the appropriate OS value for test appspecs based on runtime.
// Use this in tests instead of hardcoding "os: linux" to ensure tests pass on all platforms.
func testOS() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

// TestParseMinimalSpec verifies that a minimal valid appspec with only version
// and os parses successfully. This is the smallest valid appspec.
func TestParseMinimalSpec(t *testing.T) {
	data := []byte(fmt.Sprintf("version: 0.0\nos: %s\n", testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Version != 0.0 {
		t.Errorf("version = %v, want 0.0", spec.Version)
	}
	if spec.OS != testOS() {
		t.Errorf("os = %q, want %s", spec.OS, testOS())
	}
}

// TestParseInvalidVersion rejects versions other than 0.0, which is the only
// supported appspec version. This guards against forward-incompatible deployments.
func TestParseInvalidVersion(t *testing.T) {
	data := []byte(fmt.Sprintf("version: 1.0\nos: %s\n", testOS()))
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

// TestParseInvalidOS rejects unsupported operating system values.
func TestParseInvalidOS(t *testing.T) {
	data := []byte("version: 0.0\nos: freebsd\n")
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

// TestParseOSMismatch verifies that appspec parsing fails when the OS field
// doesn't match the runtime platform. This prevents Linux agents from attempting
// to execute Windows scripts and vice versa, matching AWS CodeDeploy behavior.
func TestParseOSMismatch(t *testing.T) {
	t.Helper()

	// Determine wrong OS for current runtime
	var wrongOS string
	if runtime.GOOS == "windows" {
		wrongOS = "linux"
	} else {
		wrongOS = "windows"
	}

	data := []byte(fmt.Sprintf("version: 0.0\nos: %s\n", wrongOS))
	_, err := Parse(data)

	if err == nil {
		t.Fatal("expected error for OS mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "platform mismatch") {
		t.Errorf("expected 'platform mismatch' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), wrongOS) {
		t.Errorf("expected error to mention wrong OS %q, got: %v", wrongOS, err)
	}
}

// TestParseOSMatch verifies that appspec parsing succeeds when the OS field
// matches the runtime platform.
func TestParseOSMatch(t *testing.T) {
	t.Helper()

	// Determine correct OS for current runtime
	var correctOS string
	if runtime.GOOS == "windows" {
		correctOS = "windows"
	} else {
		correctOS = "linux" // darwin runs tests but deploys as "linux"
	}

	data := []byte(fmt.Sprintf("version: 0.0\nos: %s\n", correctOS))
	spec, err := Parse(data)

	if err != nil {
		t.Fatalf("unexpected error for matching OS: %v", err)
	}
	if spec.OS != correctOS {
		t.Errorf("parsed OS = %q, want %q", spec.OS, correctOS)
	}
}

// TestParseHooks verifies that hooks section is correctly parsed into Script
// structs with location, timeout, runas, and sudo fields.
func TestParseHooks(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  BeforeInstall:
    - location: scripts/install.sh
      timeout: 300
      runas: root
      sudo: true
    - location: scripts/setup.sh
      timeout: 600
  AfterInstall:
    - location: scripts/verify.sh
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(spec.Hooks))
	}

	bi := spec.Hooks["BeforeInstall"]
	if len(bi) != 2 {
		t.Fatalf("expected 2 BeforeInstall scripts, got %d", len(bi))
	}
	if bi[0].Location != "scripts/install.sh" {
		t.Errorf("location = %q", bi[0].Location)
	}
	if bi[0].Timeout != 300 {
		t.Errorf("timeout = %d, want 300", bi[0].Timeout)
	}
	if bi[0].RunAs != "root" {
		t.Errorf("runas = %q, want root", bi[0].RunAs)
	}
	if !bi[0].Sudo {
		t.Error("sudo should be true")
	}

	// Second script has explicit timeout
	if bi[1].Timeout != 600 {
		t.Errorf("timeout = %d, want 600", bi[1].Timeout)
	}
}

// TestParseHookMissingLocation rejects hooks that define a script without a
// location field, which would make the script unrunnable.
func TestParseHookMissingLocation(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  BeforeInstall:
    - timeout: 300
`, testOS()))
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for missing location")
	}
}

// TestParseFiles verifies that the files section maps source to destination correctly.
func TestParseFiles(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
files:
  - source: /
    destination: /opt/app
  - source: config/db.yml
    destination: /etc/app/
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(spec.Files))
	}
	if spec.Files[0].Source != "/" || spec.Files[0].Destination != "/opt/app" {
		t.Errorf("files[0] = %+v", spec.Files[0])
	}
}

// TestParsePermissionsLinux verifies that permission entries parse correctly
// for linux targets, including owner, group, mode, and default patterns.
func TestParsePermissionsLinux(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
permissions:
  - object: /opt/app
    owner: deploy
    group: www-data
    mode: "0755"
    type:
      - file
      - directory
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Permissions) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(spec.Permissions))
	}
	p := spec.Permissions[0]
	if p.Object != "/opt/app" {
		t.Errorf("object = %q", p.Object)
	}
	if p.Owner != "deploy" {
		t.Errorf("owner = %q", p.Owner)
	}
	if p.Mode == nil || p.Mode.Value != 0755 {
		t.Errorf("mode = %+v", p.Mode)
	}
}

// TestParsePermissionsWindowsRejected ensures permissions on windows targets
// are rejected, matching the Ruby agent's behavior.
func TestParsePermissionsWindowsRejected(t *testing.T) {
	data := []byte(`
version: 0.0
os: windows
permissions:
  - object: C:\app
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for windows permissions")
	}
}

// TestParseFileExistsBehaviorValid accepts valid values.
func TestParseFileExistsBehaviorValid(t *testing.T) {
	for _, val := range []string{"DISALLOW", "OVERWRITE", "RETAIN"} {
		data := []byte(fmt.Sprintf("version: 0.0\nos: %s\nfile_exists_behavior: %s\n", testOS(), val))
		spec, err := Parse(data)
		if err != nil {
			t.Errorf("unexpected error for %q: %v", val, err)
		}
		if spec.FileExistsBehavior != val {
			t.Errorf("got %q, want %q", spec.FileExistsBehavior, val)
		}
	}
}

// TestParseFileExistsBehaviorInvalid rejects unknown behaviors.
func TestParseFileExistsBehaviorInvalid(t *testing.T) {
	data := []byte(fmt.Sprintf("version: 0.0\nos: %s\nfile_exists_behavior: UNKNOWN\n", testOS()))
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for invalid file_exists_behavior")
	}
}

// TestParseNullHooksAreSkipped verifies that null hook entries are silently
// ignored rather than causing parse errors.
func TestParseNullHooksAreSkipped(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  BeforeInstall:
  AfterInstall:
    - location: scripts/test.sh
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := spec.Hooks["BeforeInstall"]; ok {
		t.Error("null hook should be skipped")
	}
	if len(spec.Hooks["AfterInstall"]) != 1 {
		t.Error("AfterInstall should have 1 script")
	}
}

// TestParseModeOctal verifies that octal mode strings are parsed correctly.
func TestParseModeOctal(t *testing.T) {
	cases := []struct {
		input interface{}
		raw   string
		value uint32
	}{
		{"755", "755", 0755},
		{"0644", "0644", 0644},
		{644, "644", 0644},
		{"7", "007", 07},
	}
	for _, tc := range cases {
		mode, err := ParseMode(tc.input)
		if err != nil {
			t.Errorf("ParseMode(%v): %v", tc.input, err)
			continue
		}
		if mode.Raw != tc.raw {
			t.Errorf("ParseMode(%v).Raw = %q, want %q", tc.input, mode.Raw, tc.raw)
		}
		if mode.Value != tc.value {
			t.Errorf("ParseMode(%v).Value = %o, want %o", tc.input, mode.Value, tc.value)
		}
	}
}

// TestParseModeInvalid rejects mode values with non-octal digits or too many digits.
func TestParseModeInvalid(t *testing.T) {
	cases := []interface{}{"9999", "abcd", "12345"}
	for _, tc := range cases {
		_, err := ParseMode(tc)
		if err == nil {
			t.Errorf("ParseMode(%v): expected error", tc)
		}
	}
}

// TestPermissionMatchesPattern verifies glob pattern matching for permissions.
// This is the core mechanism for applying permissions to deployed files.
func TestPermissionMatchesPattern(t *testing.T) {
	p := Permission{Object: "/opt/app", Pattern: "**"}
	if !p.MatchesPattern("/opt/app/file.txt") {
		t.Error("should match with ** pattern")
	}
	if p.MatchesPattern("/other/file.txt") {
		t.Error("should not match outside object")
	}
}

// TestPermissionMatchesExcept verifies that except patterns exclude files from
// permission application.
func TestPermissionMatchesExcept(t *testing.T) {
	p := Permission{Object: "/opt/app", Except: []string{"*.log"}}
	if !p.MatchesExcept("/opt/app/debug.log") {
		t.Error("should match .log exception")
	}
	if p.MatchesExcept("/opt/app/app.conf") {
		t.Error("should not match non-log")
	}
}

// TestFindAppSpecFile_CustomFilenameNoFallback verifies that when a custom
// appspec filename is specified, FindAppSpecFile does NOT fall back to the
// default appspec.yml. The Ruby agent fails the deployment when a user-specified
// appspec file is missing; silently falling back masks a configuration error.
func TestFindAppSpecFile_CustomFilenameNoFallback(t *testing.T) {
	dir := t.TempDir()
	// Create default appspec.yml but NOT the custom filename
	if err := os.WriteFile(filepath.Join(dir, "appspec.yml"), []byte(fmt.Sprintf("version: 0.0\nos: %s\n", testOS())), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FindAppSpecFile(dir, "custom_appspec.yml")
	if err == nil {
		t.Fatal("expected error when custom appspec file is missing, should not fall back to appspec.yml")
	}
}

// TestFindAppSpecFile_CustomFilenameFound verifies that a custom appspec file
// is returned when it exists on disk.
func TestFindAppSpecFile_CustomFilenameFound(t *testing.T) {
	dir := t.TempDir()
	customFile := filepath.Join(dir, "custom_appspec.yml")
	if err := os.WriteFile(customFile, []byte(fmt.Sprintf("version: 0.0\nos: %s\n", testOS())), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := FindAppSpecFile(dir, "custom_appspec.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != customFile {
		t.Errorf("path = %q, want %q", path, customFile)
	}
}

// TestFindAppSpecFile_DefaultFallbackToYaml verifies that the default search
// order tries appspec.yml first, then appspec.yaml as a fallback.
func TestFindAppSpecFile_DefaultFallbackToYaml(t *testing.T) {
	dir := t.TempDir()
	yamlFile := filepath.Join(dir, "appspec.yaml")
	if err := os.WriteFile(yamlFile, []byte(fmt.Sprintf("version: 0.0\nos: %s\n", testOS())), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := FindAppSpecFile(dir, "appspec.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != yamlFile {
		t.Errorf("path = %q, want %q", path, yamlFile)
	}
}

// ---------------------------------------------------------------------------
// ACL tests
// ---------------------------------------------------------------------------

// TestParseACL_Valid verifies that well-formed POSIX ACL entries are accepted
// and round-trip through GetACL. ParseACL is the single entry point for all
// ACL construction in the appspec pipeline, so it must accept the standard
// tag:qualifier:perms format.
func TestParseACL_Valid(t *testing.T) {
	entries := []string{"user:deploy:rwx", "group:web:r-x", "other::r--"}
	acl, err := ParseACL(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := acl.GetACL()
	if len(got) != len(entries) {
		t.Fatalf("GetACL() returned %d entries, want %d", len(got), len(entries))
	}
	for i, e := range entries {
		if got[i] != e {
			t.Errorf("entry[%d] = %q, want %q", i, got[i], e)
		}
	}
}

// TestParseACL_InvalidEntry verifies that entries missing colons are rejected.
// The POSIX ACL format requires at least tag:perms, so a bare string is invalid.
func TestParseACL_InvalidEntry(t *testing.T) {
	_, err := ParseACL([]string{"badentry"})
	if err == nil {
		t.Fatal("expected error for entry without colons")
	}
}

// TestParseACL_SkipsBlank verifies that whitespace-only entries are silently
// dropped. This prevents empty lines in a YAML flow sequence from causing
// parse failures.
func TestParseACL_SkipsBlank(t *testing.T) {
	acl, err := ParseACL([]string{"user::rwx", "  ", "", "group::r-x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(acl.GetACL()) != 2 {
		t.Errorf("expected 2 entries after skipping blanks, got %d", len(acl.GetACL()))
	}
}

// TestACL_HasDefault verifies that entries prefixed with "d:" or "default:" are
// recognized as default ACL entries. Default ACLs are inherited by new files
// created inside a directory, so correctly detecting them is critical for the
// permission-application orchestration.
func TestACL_HasDefault(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		want    bool
	}{
		{"d: prefix", []string{"d:user::rwx"}, true},
		{"default: prefix", []string{"default:group::r-x"}, true},
		{"no default", []string{"user:deploy:rwx"}, false},
		{"mixed", []string{"user::rwx", "d:group::r-x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acl, err := ParseACL(tc.entries)
			if err != nil {
				t.Fatalf("ParseACL: %v", err)
			}
			if acl.HasDefault() != tc.want {
				t.Errorf("HasDefault() = %v, want %v", acl.HasDefault(), tc.want)
			}
		})
	}
}

// TestACL_HasBaseNamed verifies detection of non-default entries that specify a
// named user or group qualifier. Named ACL entries (e.g. user:deploy:rwx)
// require a mask entry to be effective; the agent uses HasBaseNamed to decide
// whether to auto-add one.
func TestACL_HasBaseNamed(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		want    bool
	}{
		{"named user", []string{"user:deploy:rwx"}, true},
		{"named group", []string{"group:web:r-x"}, true},
		{"short named user", []string{"u:deploy:rwx"}, true},
		{"short named group", []string{"g:web:r-x"}, true},
		{"unnamed user", []string{"user::rwx"}, false},
		{"default named ignored", []string{"d:user:deploy:rwx"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acl, err := ParseACL(tc.entries)
			if err != nil {
				t.Fatalf("ParseACL: %v", err)
			}
			if acl.HasBaseNamed() != tc.want {
				t.Errorf("HasBaseNamed() = %v, want %v", acl.HasBaseNamed(), tc.want)
			}
		})
	}
}

// TestACL_HasBaseMask verifies detection of a non-default mask entry.
// The mask controls the effective permissions for named users and groups.
func TestACL_HasBaseMask(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		want    bool
	}{
		{"mask: prefix", []string{"mask::rwx"}, true},
		{"m: prefix", []string{"m::rwx"}, true},
		{"no mask", []string{"user:deploy:rwx"}, false},
		{"default mask not base", []string{"d:mask::rwx"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acl, err := ParseACL(tc.entries)
			if err != nil {
				t.Fatalf("ParseACL: %v", err)
			}
			if acl.HasBaseMask() != tc.want {
				t.Errorf("HasBaseMask() = %v, want %v", acl.HasBaseMask(), tc.want)
			}
		})
	}
}

// TestACL_DefaultEntries exercises all five default-entry query methods in a
// single table-driven test. Each row carries one entry that should flip exactly
// one of HasDefaultUser/Group/Other/Named/Mask to true. This validates that
// the stripDefault helper correctly removes the "d:" or "default:" prefix
// before inspecting the tag.
func TestACL_DefaultEntries(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		wantUser  bool
		wantGroup bool
		wantOther bool
		wantNamed bool
		wantMask  bool
	}{
		{
			name:     "default user",
			entry:    "d:user::rwx",
			wantUser: true,
		},
		{
			name:      "default group",
			entry:     "d:group::r-x",
			wantGroup: true,
		},
		{
			name:      "default other",
			entry:     "d:other::r--",
			wantOther: true,
		},
		{
			name:      "default named user",
			entry:     "d:user:deploy:rwx",
			wantNamed: true,
		},
		{
			name:      "default named group (long prefix)",
			entry:     "default:group:web:r-x",
			wantNamed: true,
		},
		{
			name:     "default mask",
			entry:    "d:mask::rwx",
			wantMask: true,
		},
		{
			name:      "default other (long prefix)",
			entry:     "default:other::r--",
			wantOther: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acl, err := ParseACL([]string{tc.entry})
			if err != nil {
				t.Fatalf("ParseACL: %v", err)
			}
			if acl.HasDefaultUser() != tc.wantUser {
				t.Errorf("HasDefaultUser() = %v, want %v", acl.HasDefaultUser(), tc.wantUser)
			}
			if acl.HasDefaultGroup() != tc.wantGroup {
				t.Errorf("HasDefaultGroup() = %v, want %v", acl.HasDefaultGroup(), tc.wantGroup)
			}
			if acl.HasDefaultOther() != tc.wantOther {
				t.Errorf("HasDefaultOther() = %v, want %v", acl.HasDefaultOther(), tc.wantOther)
			}
			if acl.HasDefaultNamed() != tc.wantNamed {
				t.Errorf("HasDefaultNamed() = %v, want %v", acl.HasDefaultNamed(), tc.wantNamed)
			}
			if acl.HasDefaultMask() != tc.wantMask {
				t.Errorf("HasDefaultMask() = %v, want %v", acl.HasDefaultMask(), tc.wantMask)
			}
		})
	}
}

// TestACL_GetDefaultACE verifies that GetDefaultACE returns the first default
// entry. When no default entry exists it returns the empty string. This is used
// by ValidateFileACL to reject default ACLs on files.
func TestACL_GetDefaultACE(t *testing.T) {
	t.Run("returns first default entry", func(t *testing.T) {
		acl, _ := ParseACL([]string{"user::rwx", "d:group::r-x", "d:other::r--"})
		got := acl.GetDefaultACE()
		if got != "d:group::r-x" {
			t.Errorf("GetDefaultACE() = %q, want %q", got, "d:group::r-x")
		}
	})
	t.Run("empty when no defaults", func(t *testing.T) {
		acl, _ := ParseACL([]string{"user::rwx", "group::r-x"})
		if got := acl.GetDefaultACE(); got != "" {
			t.Errorf("GetDefaultACE() = %q, want empty", got)
		}
	})
}

// TestACL_GetDefaultGroupACE verifies that GetDefaultGroupACE returns the first
// default entry whose stripped tag is "g:" or "group:". This is used during
// setfacl command construction to handle default group permissions separately.
func TestACL_GetDefaultGroupACE(t *testing.T) {
	t.Run("returns first default group entry", func(t *testing.T) {
		acl, _ := ParseACL([]string{"d:user::rwx", "d:group::r-x", "d:group:web:rwx"})
		got := acl.GetDefaultGroupACE()
		if got != "d:group::r-x" {
			t.Errorf("GetDefaultGroupACE() = %q, want %q", got, "d:group::r-x")
		}
	})
	t.Run("empty when no default group", func(t *testing.T) {
		acl, _ := ParseACL([]string{"d:user::rwx", "group::r-x"})
		if got := acl.GetDefaultGroupACE(); got != "" {
			t.Errorf("GetDefaultGroupACE() = %q, want empty", got)
		}
	})
}

// TestACL_AddEntry verifies that AddEntry appends a new entry to the ACL.
// The agent adds computed entries (e.g., auto-mask) before calling setfacl.
func TestACL_AddEntry(t *testing.T) {
	acl, _ := ParseACL([]string{"user::rwx"})
	before := len(acl.GetACL())
	acl.AddEntry("mask::rwx")
	after := len(acl.GetACL())
	if after != before+1 {
		t.Errorf("entry count after AddEntry = %d, want %d", after, before+1)
	}
	if acl.GetACL()[after-1] != "mask::rwx" {
		t.Errorf("last entry = %q, want %q", acl.GetACL()[after-1], "mask::rwx")
	}
}

// TestACL_ClearAdditional verifies that ClearAdditional truncates back to the
// original entry count. After setfacl execution the agent may have added mask
// or default entries; ClearAdditional restores the ACL to its parsed state so
// re-application is idempotent.
func TestACL_ClearAdditional(t *testing.T) {
	acl, _ := ParseACL([]string{"user::rwx", "group::r-x"})
	original := len(acl.GetACL())
	acl.AddEntry("mask::rwx")
	acl.AddEntry("d:user::rwx")
	if len(acl.GetACL()) != original+2 {
		t.Fatalf("expected %d entries after adds, got %d", original+2, len(acl.GetACL()))
	}
	acl.ClearAdditional(original)
	if len(acl.GetACL()) != original {
		t.Errorf("after ClearAdditional: count = %d, want %d", len(acl.GetACL()), original)
	}
}

// TestACL_ClearAdditional_NoOpWhenLarger verifies that ClearAdditional is a
// no-op when the requested count exceeds the current length. This guards
// against accidental truncation if the caller passes a stale count.
func TestACL_ClearAdditional_NoOpWhenLarger(t *testing.T) {
	acl, _ := ParseACL([]string{"user::rwx"})
	acl.ClearAdditional(100)
	if len(acl.GetACL()) != 1 {
		t.Errorf("count = %d, want 1 (should be unchanged)", len(acl.GetACL()))
	}
}

// ---------------------------------------------------------------------------
// SELinuxRange tests
// ---------------------------------------------------------------------------

// TestSELinuxRange_GetRange verifies the formatted output of GetRange.
// When High is set the range is "low-high" (MLS range); when absent the range
// is just the low value. This format is consumed by semanage fcontext.
func TestSELinuxRange_GetRange(t *testing.T) {
	t.Run("with high", func(t *testing.T) {
		r := SELinuxRange{Low: "s0", High: "s0:c0.c1023"}
		if got := r.GetRange(); got != "s0-s0:c0.c1023" {
			t.Errorf("GetRange() = %q, want %q", got, "s0-s0:c0.c1023")
		}
	})
	t.Run("low only", func(t *testing.T) {
		r := SELinuxRange{Low: "s0"}
		if got := r.GetRange(); got != "s0" {
			t.Errorf("GetRange() = %q, want %q", got, "s0")
		}
	})
}

// ---------------------------------------------------------------------------
// ParseContext tested through Parse (rawContext is unexported)
// ---------------------------------------------------------------------------

// TestParse_WithContext verifies that the SELinux context section in a
// permissions entry is correctly parsed through the full YAML pipeline.
// Since rawContext is unexported, this is the only way to exercise
// ParseContext from a test.
func TestParse_WithContext(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
files:
  - source: /
    destination: /opt/app
permissions:
  - object: /opt/app
    pattern: "**"
    type:
      - file
    context:
      user: system_u
      role: object_r
      type: httpd_sys_content_t
      range:
        low: s0
        high: s0:c0.c1023
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Permissions) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(spec.Permissions))
	}
	ctx := spec.Permissions[0].Context
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if ctx.User != "system_u" {
		t.Errorf("context.User = %q, want %q", ctx.User, "system_u")
	}
	if ctx.Role != "object_r" {
		t.Errorf("context.Role = %q, want %q", ctx.Role, "object_r")
	}
	if ctx.Type != "httpd_sys_content_t" {
		t.Errorf("context.Type = %q, want %q", ctx.Type, "httpd_sys_content_t")
	}
	if ctx.Range == nil {
		t.Fatal("context.Range should not be nil")
	}
	if got := ctx.Range.GetRange(); got != "s0-s0:c0.c1023" {
		t.Errorf("Range.GetRange() = %q, want %q", got, "s0-s0:c0.c1023")
	}
}

// TestParse_WithContextLowOnly verifies that an SELinux context with only a
// low sensitivity level (no high) parses correctly and GetRange returns just
// the low value.
func TestParse_WithContextLowOnly(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
files:
  - source: /
    destination: /opt/app
permissions:
  - object: /opt/app
    pattern: "**"
    type:
      - file
    context:
      type: httpd_sys_content_t
      range:
        low: s0
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx := spec.Permissions[0].Context
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if ctx.Range == nil {
		t.Fatal("context.Range should not be nil")
	}
	if got := ctx.Range.GetRange(); got != "s0" {
		t.Errorf("Range.GetRange() = %q, want %q", got, "s0")
	}
}

// TestParse_WithContextMissingType verifies that an SELinux context without a
// type field is rejected. The type is the minimum required field because
// semanage fcontext always needs it.
func TestParse_WithContextMissingType(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
permissions:
  - object: /opt/app
    type:
      - file
    context:
      user: system_u
`, testOS()))
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for context without type")
	}
}

// ---------------------------------------------------------------------------
// Permission validation tests
// ---------------------------------------------------------------------------

// TestValidateFilePermission_Valid verifies that a file-type permission with
// the default "**" pattern and no except list passes validation. This is the
// normal case for a file permission entry.
func TestValidateFilePermission_Valid(t *testing.T) {
	p := Permission{
		Object:  "/opt/app/config.yml",
		Pattern: "**",
		Owner:   "deploy",
		Type:    []string{"file"},
	}
	if err := p.ValidateFilePermission(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateFilePermission_RejectsPattern verifies that file-type permissions
// reject non-default patterns. Patterns only make sense for directories (to
// match children); a file permission targets a single object.
func TestValidateFilePermission_RejectsPattern(t *testing.T) {
	p := Permission{
		Object:  "/opt/app",
		Pattern: "*.conf",
		Type:    []string{"file"},
	}
	if err := p.ValidateFilePermission(); err == nil {
		t.Fatal("expected error for file permission with non-default pattern")
	}
}

// TestValidateFilePermission_RejectsExcept verifies that file-type permissions
// reject except lists for the same reason as non-default patterns.
func TestValidateFilePermission_RejectsExcept(t *testing.T) {
	p := Permission{
		Object:  "/opt/app",
		Pattern: "**",
		Except:  []string{"*.log"},
		Type:    []string{"file"},
	}
	if err := p.ValidateFilePermission(); err == nil {
		t.Fatal("expected error for file permission with except list")
	}
}

// TestValidateFilePermission_DirectoryTypeSkipped verifies that a directory-only
// permission can use patterns and except lists without error, because the
// validation only fires for file-type entries.
func TestValidateFilePermission_DirectoryTypeSkipped(t *testing.T) {
	p := Permission{
		Object:  "/opt/app",
		Pattern: "*.conf",
		Except:  []string{"secret.conf"},
		Type:    []string{"directory"},
	}
	if err := p.ValidateFilePermission(); err != nil {
		t.Errorf("directory permission should not be rejected: %v", err)
	}
}

// TestValidateFileACL_Valid verifies that a file permission without default ACL
// entries passes validation.
func TestValidateFileACL_Valid(t *testing.T) {
	acl, _ := ParseACL([]string{"user:deploy:rwx", "group:web:r-x"})
	p := Permission{
		Object: "/opt/app/file.txt",
		ACLs:   &acl,
		Type:   []string{"file"},
	}
	if err := p.ValidateFileACL(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateFileACL_RejectsDefaultACL verifies that default ACL entries are
// rejected on file permissions. Default ACLs are only meaningful on directories
// (they set the inherited ACL for new children); applying them to a file is a
// configuration error.
func TestValidateFileACL_RejectsDefaultACL(t *testing.T) {
	acl, _ := ParseACL([]string{"d:user::rwx"})
	p := Permission{
		Object: "/opt/app/file.txt",
		ACLs:   &acl,
		Type:   []string{"file"},
	}
	if err := p.ValidateFileACL(); err == nil {
		t.Fatal("expected error for default ACL on file permission")
	}
}

// TestValidateFileACL_NilACLs verifies that a permission with no ACLs at all
// passes validation (ACLs are optional).
func TestValidateFileACL_NilACLs(t *testing.T) {
	p := Permission{Object: "/opt/app/file.txt", Type: []string{"file"}}
	if err := p.ValidateFileACL(); err != nil {
		t.Errorf("unexpected error for nil ACLs: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParseFile tests
// ---------------------------------------------------------------------------

// TestParseFile_ValidAppspec writes a valid appspec to a temp file and verifies
// that ParseFile reads and parses it. This covers the os.ReadFile + Parse
// composition that is the main entry point for deployment processing.
func TestParseFile_ValidAppspec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appspec.yml")
	content := []byte(fmt.Sprintf(`
version: 0.0
os: %s
files:
  - source: /
    destination: /opt/app
permissions:
  - object: /opt/app
    owner: deploy
    group: web
    mode: "0755"
    type:
      - file
`, testOS()))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if spec.OS != testOS() {
		t.Errorf("OS = %q, want %s", spec.OS, testOS())
	}
	if len(spec.Files) != 1 {
		t.Errorf("Files count = %d, want 1", len(spec.Files))
	}
	if len(spec.Permissions) != 1 {
		t.Errorf("Permissions count = %d, want 1", len(spec.Permissions))
	}
	if spec.Permissions[0].Owner != "deploy" {
		t.Errorf("owner = %q, want deploy", spec.Permissions[0].Owner)
	}
}

// TestParseFile_NotFound verifies that ParseFile returns an error when the
// path does not exist. The agent must surface a clear error rather than
// panic on missing files.
func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/appspec.yml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

// ---------------------------------------------------------------------------
// Parse integration tests for permissions, hooks, and ACLs via YAML
// ---------------------------------------------------------------------------

// TestParse_WithPermissions verifies the full permissions section round-trip
// including owner, group, mode, pattern, and type fields. This is a broader
// integration test than TestParsePermissionsLinux because it also checks that
// group and the default pattern are populated.
func TestParse_WithPermissions(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
files:
  - source: /
    destination: /opt/app
permissions:
  - object: /opt/app
    pattern: "**"
    owner: deploy
    group: web
    mode: "0755"
    type:
      - file
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Permissions) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(spec.Permissions))
	}
	p := spec.Permissions[0]
	if p.Group != "web" {
		t.Errorf("group = %q, want web", p.Group)
	}
	if p.Pattern != "**" {
		t.Errorf("pattern = %q, want **", p.Pattern)
	}
	if len(p.Type) != 1 || p.Type[0] != "file" {
		t.Errorf("type = %v, want [file]", p.Type)
	}
}

// TestParse_WithHookTimeout verifies that a custom timeout value on a hook
// script is preserved through parsing. The default is 3600s; a deployment
// author may set a shorter or longer timeout for slow/fast scripts.
func TestParse_WithHookTimeout(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  ApplicationStart:
    - location: scripts/start.sh
      timeout: 120
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scripts := spec.Hooks["ApplicationStart"]
	if len(scripts) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scripts))
	}
	if scripts[0].Timeout != 120 {
		t.Errorf("timeout = %d, want 120", scripts[0].Timeout)
	}
}

// TestParse_WithHookSudo verifies that runas and sudo fields on a hook script
// are correctly parsed. These control privilege escalation during lifecycle
// hook execution.
func TestParse_WithHookSudo(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  ValidateService:
    - location: scripts/validate.sh
      runas: deploy
      sudo: true
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scripts := spec.Hooks["ValidateService"]
	if len(scripts) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scripts))
	}
	if scripts[0].RunAs != "deploy" {
		t.Errorf("runas = %q, want deploy", scripts[0].RunAs)
	}
	if !scripts[0].Sudo {
		t.Error("sudo should be true")
	}
}

// TestParse_WithACLs verifies that the acls section inside a permission entry
// is parsed through the full YAML pipeline and produces a valid ACL with
// the expected entries. The rawACL struct uses a nested "entries" key.
func TestParse_WithACLs(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
permissions:
  - object: /opt/app
    type:
      - directory
    acls:
      entries:
        - user:deploy:rwx
        - group:web:r-x
        - d:user::rwx
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := spec.Permissions[0]
	if p.ACLs == nil {
		t.Fatal("ACLs should not be nil")
	}
	if len(p.ACLs.GetACL()) != 3 {
		t.Errorf("ACL entry count = %d, want 3", len(p.ACLs.GetACL()))
	}
	if !p.ACLs.HasBaseNamed() {
		t.Error("should have base named entry")
	}
	if !p.ACLs.HasDefault() {
		t.Error("should have default entry")
	}
}

// ---------------------------------------------------------------------------
// Platform-specific hook validation tests
// ---------------------------------------------------------------------------

// TestParse_RunAsOnWindowsRejects verifies that appspec files specifying runas
// on Windows are rejected at parse time. AWS CodeDeploy does not support runas
// on Windows Server, so this prevents deployments that would fail at runtime.
func TestParse_RunAsOnWindowsRejects(t *testing.T) {
	// Only test on Windows runtime; on other platforms the OS mismatch error
	// happens first (which is also correct behavior)
	if runtime.GOOS != "windows" {
		t.Skip("test only runs on Windows")
	}
	data := []byte(`
version: 0.0
os: windows
hooks:
  ApplicationStart:
    - location: scripts/start.bat
      runas: Administrator
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for runas on Windows")
	}
	if !strings.Contains(err.Error(), "runas is not supported on Windows") {
		t.Errorf("expected runas error message, got: %v", err)
	}
}

// TestParse_CumulativeTimeoutExceedsLimit verifies that lifecycle events with
// total script timeouts exceeding 3600 seconds are rejected. AWS enforces this
// limit to prevent indefinitely hanging deployments.
func TestParse_CumulativeTimeoutExceedsLimit(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  ApplicationStart:
    - location: scripts/start1.sh
      timeout: 1000
    - location: scripts/start2.sh
      timeout: 1000
    - location: scripts/start3.sh
      timeout: 1000
    - location: scripts/start4.sh
      timeout: 1000
`, testOS()))
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for cumulative timeout > 3600s")
	}
	if !strings.Contains(err.Error(), "exceeds maximum of 3600 seconds") {
		t.Errorf("expected cumulative timeout error, got: %v", err)
	}
}

// TestParse_CumulativeTimeoutAtLimit verifies that lifecycle events with total
// script timeouts exactly at 3600 seconds are accepted.
func TestParse_CumulativeTimeoutAtLimit(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  ApplicationStart:
    - location: scripts/start1.sh
      timeout: 1200
    - location: scripts/start2.sh
      timeout: 1200
    - location: scripts/start3.sh
      timeout: 1200
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error for cumulative timeout at 3600s: %v", err)
	}
	scripts := spec.Hooks["ApplicationStart"]
	if len(scripts) != 3 {
		t.Errorf("expected 3 scripts, got %d", len(scripts))
	}
}

// TestParse_CumulativeTimeoutBelowLimit verifies that lifecycle events with
// total script timeouts below 3600 seconds are accepted.
func TestParse_CumulativeTimeoutBelowLimit(t *testing.T) {
	data := []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  BeforeInstall:
    - location: scripts/prep1.sh
      timeout: 300
    - location: scripts/prep2.sh
      timeout: 600
`, testOS()))
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error for cumulative timeout < 3600s: %v", err)
	}
	scripts := spec.Hooks["BeforeInstall"]
	if len(scripts) != 2 {
		t.Errorf("expected 2 scripts, got %d", len(scripts))
	}
}
