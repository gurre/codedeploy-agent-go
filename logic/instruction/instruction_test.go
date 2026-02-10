package instruction

import (
	"testing"

	json "github.com/goccy/go-json"
)

// TestBuilderCopyAndMkdirAccumulate verifies that Copy and Mkdir calls add
// the correct command types in order.
func TestBuilderCopyAndMkdirAccumulate(t *testing.T) {
	b := NewBuilder()
	if err := b.Mkdir("/opt/app"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := b.Copy("/src/file.txt", "/opt/app/file.txt"); err != nil {
		t.Fatalf("copy: %v", err)
	}

	cmds := b.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	if cmds[0].Type != TypeMkdir {
		t.Errorf("cmd[0].Type = %q, want mkdir", cmds[0].Type)
	}
	if cmds[1].Type != TypeCopy {
		t.Errorf("cmd[1].Type = %q, want copy", cmds[1].Type)
	}
}

// TestBuilderDuplicateCopyReturnsError verifies that copying two files to the
// same destination is detected and rejected, preventing silent overwrites.
func TestBuilderDuplicateCopyReturnsError(t *testing.T) {
	b := NewBuilder()
	if err := b.Copy("/a", "/dest"); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	err := b.Copy("/b", "/dest")
	if err == nil {
		t.Fatal("expected error for duplicate copy")
	}
}

// TestBuilderMkdirIdempotent verifies that calling Mkdir twice for the same
// path only creates one command. Directory creation is idempotent.
func TestBuilderMkdirIdempotent(t *testing.T) {
	b := NewBuilder()
	if err := b.Mkdir("/opt/app"); err != nil {
		t.Fatalf("mkdir 1: %v", err)
	}
	if err := b.Mkdir("/opt/app"); err != nil {
		t.Fatalf("mkdir 2: %v", err)
	}
	if len(b.Commands()) != 1 {
		t.Errorf("expected 1 mkdir command, got %d", len(b.Commands()))
	}
}

// TestBuilderCopyMkdirConflict verifies that copy and mkdir to the same path
// are detected as a conflict.
func TestBuilderCopyMkdirConflict(t *testing.T) {
	b := NewBuilder()
	if err := b.Copy("/src", "/dest"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	err := b.Mkdir("/dest")
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

// TestBuildToJSONRoundTrips verifies that instructions survive JSON
// serialization and deserialization.
func TestBuildToJSONRoundTrips(t *testing.T) {
	b := NewBuilder()
	_ = b.Mkdir("/opt")
	_ = b.Copy("/src/f", "/opt/f")
	b.SetMode("/opt/f", "0755")
	b.SetOwner("/opt/f", "root", "root")

	inst := b.Build()
	data, err := inst.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	cmds, err := ParseInstallCommands(data)
	if err != nil {
		t.Fatalf("ParseInstallCommands: %v", err)
	}
	if len(cmds) != 4 {
		t.Fatalf("expected 4 commands, got %d", len(cmds))
	}
}

// TestParseRemoveCommandsReversesOrder verifies that cleanup entries are
// returned in reverse order for bottom-up removal.
func TestParseRemoveCommandsReversesOrder(t *testing.T) {
	data := "/opt/app/file.txt\n/opt/app\n"
	entries := ParseRemoveCommands(data)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Path != "/opt/app" {
		t.Errorf("entries[0] = %q, want /opt/app", entries[0].Path)
	}
	if entries[1].Path != "/opt/app/file.txt" {
		t.Errorf("entries[1] = %q, want /opt/app/file.txt", entries[1].Path)
	}
}

// TestParseRemoveCommandsDiscardsPartialLastLine verifies that incomplete
// writes are safely ignored, preventing removal of wrong paths.
func TestParseRemoveCommandsDiscardsPartialLastLine(t *testing.T) {
	data := "/opt/complete\n/opt/incomple"
	entries := ParseRemoveCommands(data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "/opt/complete" {
		t.Errorf("path = %q", entries[0].Path)
	}
}

// TestParseRemoveCommandsSemanageEntry verifies that semanage entries are
// correctly detected and their path extracted.
func TestParseRemoveCommandsSemanageEntry(t *testing.T) {
	data := "semanage\x00/opt/app\n/opt/file\n"
	entries := ParseRemoveCommands(data)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Reversed: file first, then semanage
	if entries[0].Path != "/opt/file" {
		t.Errorf("entries[0] = %q", entries[0].Path)
	}
	if !entries[1].IsContext {
		t.Error("entries[1] should be a context entry")
	}
	if entries[1].Path != "/opt/app" {
		t.Errorf("entries[1].Path = %q", entries[1].Path)
	}
}

// TestParseRemoveCommandsEmptyInput returns nil for empty data.
func TestParseRemoveCommandsEmptyInput(t *testing.T) {
	entries := ParseRemoveCommands("")
	if entries != nil {
		t.Errorf("expected nil, got %v", entries)
	}
}

// TestInstructionsJSONFormat verifies the JSON structure matches what the
// Ruby agent produces, ensuring compatibility.
func TestInstructionsJSONFormat(t *testing.T) {
	inst := Instructions{
		Commands: []Command{
			{Type: TypeCopy, Source: "/src/f", Destination: "/dst/f"},
			{Type: TypeMkdir, Directory: "/dst/d"},
		},
	}
	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	instructions, ok := m["instructions"]
	if !ok {
		t.Fatal("missing 'instructions' key")
	}
	arr, ok := instructions.([]interface{})
	if !ok {
		t.Fatal("instructions is not an array")
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 instructions, got %d", len(arr))
	}
}

// TestIsCopyAndMkdirTarget verifies the target tracking functions used for
// permission matching.
func TestIsCopyAndMkdirTarget(t *testing.T) {
	b := NewBuilder()
	_ = b.Copy("/src", "/dst/file")
	_ = b.Mkdir("/dst/dir")

	if !b.IsCopyTarget("/dst/file") {
		t.Error("should be copy target")
	}
	if b.IsCopyTarget("/dst/other") {
		t.Error("should not be copy target")
	}
	if !b.IsMkdirTarget("/dst/dir") {
		t.Error("should be mkdir target")
	}
}

// TestBuilderSetACL verifies that SetACL creates a setfacl command with the
// correct file path and ACL entries. ACL commands are generated from the
// permissions section of the appspec.
func TestBuilderSetACL(t *testing.T) {
	b := NewBuilder()
	acl := []string{"user::rwx", "group::r-x", "other::r--"}
	b.SetACL("/opt/app/file.txt", acl)

	cmds := b.Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != TypeSetfacl {
		t.Errorf("Type = %q, want setfacl", cmds[0].Type)
	}
	if cmds[0].File != "/opt/app/file.txt" {
		t.Errorf("File = %q, want /opt/app/file.txt", cmds[0].File)
	}
	if len(cmds[0].ACL) != 3 {
		t.Errorf("ACL length = %d, want 3", len(cmds[0].ACL))
	}
}

// TestBuilderSetContext verifies that SetContext creates a semanage command
// with the correct SELinux context fields. This is needed for deployments
// on systems with SELinux enabled.
func TestBuilderSetContext(t *testing.T) {
	b := NewBuilder()
	ctx := ContextCmd{
		User:  "system_u",
		Role:  "object_r",
		Type:  "httpd_sys_content_t",
		Range: "s0",
	}
	b.SetContext("/var/www/html/index.html", ctx)

	cmds := b.Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != TypeSemanage {
		t.Errorf("Type = %q, want semanage", cmds[0].Type)
	}
	if cmds[0].Context == nil {
		t.Fatal("Context is nil")
	}
	if cmds[0].Context.Type != "httpd_sys_content_t" {
		t.Errorf("Context.Type = %q, want httpd_sys_content_t", cmds[0].Context.Type)
	}
}

// TestBuilderMarkPermission verifies that MarkPermission tracks paths and
// rejects duplicate permission applications. Without this guard, the installer
// could apply the same permission set twice to a file.
func TestBuilderMarkPermission(t *testing.T) {
	b := NewBuilder()
	if err := b.MarkPermission("/opt/app/file.txt"); err != nil {
		t.Fatalf("first MarkPermission: %v", err)
	}
	err := b.MarkPermission("/opt/app/file.txt")
	if err == nil {
		t.Fatal("expected error for duplicate MarkPermission")
	}
}

// TestBuilderCopyTargets verifies that CopyTargets returns all registered copy
// destinations. The installer uses this to match permissions against copied files.
func TestBuilderCopyTargets(t *testing.T) {
	b := NewBuilder()
	_ = b.Copy("/src/a", "/dst/a")
	_ = b.Copy("/src/b", "/dst/b")

	targets := b.CopyTargets()
	if len(targets) != 2 {
		t.Fatalf("expected 2 copy targets, got %d", len(targets))
	}
	seen := map[string]bool{}
	for _, p := range targets {
		seen[p] = true
	}
	if !seen["/dst/a"] || !seen["/dst/b"] {
		t.Errorf("CopyTargets = %v, want /dst/a and /dst/b", targets)
	}
}

// TestBuilderMkdirTargets verifies that MkdirTargets returns all registered
// directory targets. The installer uses this to match permissions against
// created directories.
func TestBuilderMkdirTargets(t *testing.T) {
	b := NewBuilder()
	_ = b.Mkdir("/dst/dir1")
	_ = b.Mkdir("/dst/dir2")

	targets := b.MkdirTargets()
	if len(targets) != 2 {
		t.Fatalf("expected 2 mkdir targets, got %d", len(targets))
	}
	seen := map[string]bool{}
	for _, p := range targets {
		seen[p] = true
	}
	if !seen["/dst/dir1"] || !seen["/dst/dir2"] {
		t.Errorf("MkdirTargets = %v, want /dst/dir1 and /dst/dir2", targets)
	}
}

// TestBuilderMkdirCopyConflict verifies that adding a mkdir before a copy
// to the same path correctly detects the conflict (inverse of TestBuilderCopyMkdirConflict).
func TestBuilderMkdirCopyConflict(t *testing.T) {
	b := NewBuilder()
	if err := b.Mkdir("/dest"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	err := b.Copy("/src", "/dest")
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

// TestParseInstallCommandsInvalidJSON verifies that malformed JSON returns
// an error instead of silently producing empty instructions.
func TestParseInstallCommandsInvalidJSON(t *testing.T) {
	_, err := ParseInstallCommands([]byte("{not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
