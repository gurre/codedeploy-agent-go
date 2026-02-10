package localcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultOptions verifies defaults match the Ruby CLI's defaults.
// The Ruby agent uses "directory" as bundle type, "DISALLOW" as FEB,
// and "default-local-deployment-group" as the group name.
func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	if opts.BundleType != "directory" {
		t.Errorf("BundleType = %q, want directory", opts.BundleType)
	}
	if opts.FileExistsBehavior != "DISALLOW" {
		t.Errorf("FileExistsBehavior = %q, want DISALLOW", opts.FileExistsBehavior)
	}
	if opts.DeploymentGroup != "default-local-deployment-group" {
		t.Errorf("DeploymentGroup = %q, want default-local-deployment-group", opts.DeploymentGroup)
	}
	if opts.DeploymentGroupName != "LocalFleet" {
		t.Errorf("DeploymentGroupName = %q, want LocalFleet", opts.DeploymentGroupName)
	}
	if opts.AppSpecFilename != "appspec.yml" {
		t.Errorf("AppSpecFilename = %q, want appspec.yml", opts.AppSpecFilename)
	}
}

// TestValidate_InvalidBundleType verifies that unsupported bundle types
// are rejected before any deployment work begins.
func TestValidate_InvalidBundleType(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/tmp"
	opts.BundleType = "rpm"
	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for invalid bundle type")
	}
	if !strings.Contains(err.Error(), "invalid bundle type") {
		t.Errorf("error = %v, want mention of invalid bundle type", err)
	}
}

// TestValidate_InvalidFileExistsBehavior verifies that invalid FEB values
// are caught during validation rather than silently ignored during install.
func TestValidate_InvalidFileExistsBehavior(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/tmp"
	opts.FileExistsBehavior = "REPLACE"
	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for invalid file-exists-behavior")
	}
	if !strings.Contains(err.Error(), "invalid file-exists-behavior") {
		t.Errorf("error = %v, want mention of file-exists-behavior", err)
	}
}

// TestValidate_MissingBundleLocation verifies that an empty bundle location
// is rejected. The bundle location is the primary required input.
func TestValidate_MissingBundleLocation(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = ""
	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for missing bundle location")
	}
	if !strings.Contains(err.Error(), "bundle location required") {
		t.Errorf("error = %v, want mention of bundle location required", err)
	}
}

// TestValidate_DirectoryBundle verifies that directory bundles must contain
// an appspec.yml or appspec.yaml file. Without it the deployment would fail
// later with a confusing error.
func TestValidate_DirectoryBundle(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions()
	opts.BundleLocation = dir
	opts.BundleType = "directory"

	// No appspec file -> error
	err := validate(opts)
	if err == nil {
		t.Fatal("expected error when appspec is missing from directory bundle")
	}

	// Add appspec.yml -> success
	if err := os.WriteFile(filepath.Join(dir, "appspec.yml"), []byte("version: 0.0\nos: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = validate(opts)
	if err != nil {
		t.Fatalf("validate should pass with appspec.yml present: %v", err)
	}
}

// TestValidate_CustomAppSpecFilename verifies that when AppSpecFilename is set
// to a non-default value, validate checks for that specific file rather than
// the standard appspec.yml/appspec.yaml. This is the L14/L16/L17/L18 bug fix:
// the Ruby agent allows --appspec-filename to override the expected filename.
func TestValidate_CustomAppSpecFilename(t *testing.T) {
	dir := t.TempDir()

	// Create a custom appspec file (not the default name)
	if err := os.WriteFile(filepath.Join(dir, "appspec_override.yaml"), []byte("version: 0.0\nos: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.BundleLocation = dir
	opts.BundleType = "directory"
	opts.AppSpecFilename = "appspec_override.yaml"

	// Should pass because the custom file exists
	if err := validate(opts); err != nil {
		t.Fatalf("validate should pass with custom appspec present: %v", err)
	}
}

// TestValidate_CustomAppSpecFilename_Missing verifies that when a custom
// appspec filename is specified but the file does not exist, validation fails
// with a message naming the expected file.
func TestValidate_CustomAppSpecFilename_Missing(t *testing.T) {
	dir := t.TempDir()

	// Create only the default appspec — but request a different name
	if err := os.WriteFile(filepath.Join(dir, "appspec.yml"), []byte("version: 0.0\nos: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.BundleLocation = dir
	opts.BundleType = "directory"
	opts.AppSpecFilename = "appspec_override.yaml"

	err := validate(opts)
	if err == nil {
		t.Fatal("expected error when custom appspec file is missing")
	}
	if !strings.Contains(err.Error(), "appspec_override.yaml") {
		t.Errorf("error should mention the custom filename, got: %v", err)
	}
}

// TestValidate_DefaultAppSpecFallback verifies that the standard appspec.yml
// and appspec.yaml filenames still work when AppSpecFilename is set to one
// of the defaults (or empty). Ensures the fix doesn't break the common case.
func TestValidate_DefaultAppSpecFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "appspec.yaml"), []byte("version: 0.0\nos: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.BundleLocation = dir
	opts.BundleType = "directory"
	// AppSpecFilename defaults to "appspec.yml" — but appspec.yaml exists
	if err := validate(opts); err != nil {
		t.Fatalf("validate should pass with appspec.yaml present: %v", err)
	}
}

// TestResolveEvents_Default verifies that when no events are specified,
// the full default lifecycle sequence is returned.
func TestResolveEvents_Default(t *testing.T) {
	events := resolveEvents(nil)
	if len(events) != len(defaultOrderedEvents) {
		t.Errorf("len(events) = %d, want %d", len(events), len(defaultOrderedEvents))
	}
	for i, e := range events {
		if e != defaultOrderedEvents[i] {
			t.Errorf("events[%d] = %q, want %q", i, e, defaultOrderedEvents[i])
		}
	}
}

// TestResolveEvents_DownloadAndInstallPrecedeUserEvents verifies that
// DownloadBundle and Install are always placed before user events. The Ruby
// agent (deployer.rb:105-118) prepends these infrastructure events so that the
// bundle is downloaded and files are installed before any user hooks run.
func TestResolveEvents_DownloadAndInstallPrecedeUserEvents(t *testing.T) {
	events := resolveEvents([]string{"BeforeInstall", "ApplicationStart"})
	expected := []string{"DownloadBundle", "Install", "BeforeInstall", "ApplicationStart"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, e := range events {
		if e != expected[i] {
			t.Errorf("events[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

// TestResolveEvents_UserIncludesInfraEvents verifies that when the user
// explicitly specifies DownloadBundle or Install, they are not duplicated.
func TestResolveEvents_UserIncludesInfraEvents(t *testing.T) {
	events := resolveEvents([]string{"DownloadBundle", "Install", "AfterInstall"})
	expected := []string{"DownloadBundle", "Install", "AfterInstall"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, e := range events {
		if e != expected[i] {
			t.Errorf("events[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

// TestResolveEvents_CustomEvent verifies that non-standard event names are
// preserved after the infrastructure events. The Ruby agent dynamically merges
// custom events into the hook mapping.
func TestResolveEvents_CustomEvent(t *testing.T) {
	events := resolveEvents([]string{"CustomEvent"})
	expected := []string{"DownloadBundle", "Install", "CustomEvent"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, e := range events {
		if e != expected[i] {
			t.Errorf("events[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

// TestBuildSpec_Directory verifies that a directory bundle produces a spec
// with RevisionLocalDirectory source and the correct bundle type.
func TestBuildSpec_Directory(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/opt/myapp"
	opts.BundleType = "directory"
	opts.ApplicationName = "myapp"

	spec, err := buildSpec(opts)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}

	if spec.Source != "Local Directory" {
		t.Errorf("Source = %q, want Local Directory", spec.Source)
	}
	if spec.BundleType != "directory" {
		t.Errorf("BundleType = %q, want directory", spec.BundleType)
	}
	if spec.LocalLocation != "/opt/myapp" {
		t.Errorf("LocalLocation = %q, want /opt/myapp", spec.LocalLocation)
	}
	if !strings.HasPrefix(spec.DeploymentID, "d-") {
		t.Errorf("DeploymentID should start with d-, got %q", spec.DeploymentID)
	}
}

// TestBuildSpec_Archive verifies that a non-directory bundle produces a spec
// with RevisionLocalFile source.
func TestBuildSpec_Archive(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/tmp/app.tar"
	opts.BundleType = "tar"

	spec, err := buildSpec(opts)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}

	if spec.Source != "Local File" {
		t.Errorf("Source = %q, want Local File", spec.Source)
	}
	if spec.BundleType != "tar" {
		t.Errorf("BundleType = %q, want tar", spec.BundleType)
	}
}

// TestBuildSpec_S3URL verifies that an s3:// URL is parsed into RevisionS3
// with correct bucket, key, and bundle type fields. This enables the L8
// scenario where the local CLI fetches a bundle from S3.
func TestBuildSpec_S3URL(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "s3://my-bucket/releases/app.zip"
	opts.BundleType = "zip"

	spec, err := buildSpec(opts)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Source != "S3" {
		t.Errorf("Source = %q, want S3", spec.Source)
	}
	if spec.Bucket != "my-bucket" {
		t.Errorf("Bucket = %q, want my-bucket", spec.Bucket)
	}
	if spec.Key != "releases/app.zip" {
		t.Errorf("Key = %q, want releases/app.zip", spec.Key)
	}
	if spec.BundleType != "zip" {
		t.Errorf("BundleType = %q, want zip", spec.BundleType)
	}
}

// TestBuildSpec_S3URLWithQueryParams verifies that versionId and etag query
// parameters from S3 URLs are extracted into the spec. The Ruby agent parses
// these from the s3:// URL (deployer.rb:183-208).
func TestBuildSpec_S3URLWithQueryParams(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "s3://my-bucket/app.tar?versionId=ver1&etag=etag1"
	opts.BundleType = "tar"

	spec, err := buildSpec(opts)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Version != "ver1" {
		t.Errorf("Version = %q, want ver1", spec.Version)
	}
	if spec.ETag != "etag1" {
		t.Errorf("ETag = %q, want etag1", spec.ETag)
	}
}

// TestParseS3URL verifies correct parsing of s3://bucket/key URLs with and
// without query parameters. This is fundamental to S3-sourced local deployments.
func TestParseS3URL(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		bucket    string
		key       string
		versionID string
		etag      string
	}{
		{
			name:   "simple path",
			input:  "s3://my-bucket/path/to/app.tar",
			bucket: "my-bucket",
			key:    "path/to/app.tar",
		},
		{
			name:      "with query params",
			input:     "s3://my-bucket/app.zip?versionId=v123&etag=abc",
			bucket:    "my-bucket",
			key:       "app.zip",
			versionID: "v123",
			etag:      "abc",
		},
		{
			name:   "nested key",
			input:  "s3://bucket/a/b/c/d.tgz",
			bucket: "bucket",
			key:    "a/b/c/d.tgz",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bucket, key, versionID, etag, err := parseS3URL(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tc.bucket {
				t.Errorf("bucket = %q, want %q", bucket, tc.bucket)
			}
			if key != tc.key {
				t.Errorf("key = %q, want %q", key, tc.key)
			}
			if versionID != tc.versionID {
				t.Errorf("versionID = %q, want %q", versionID, tc.versionID)
			}
			if etag != tc.etag {
				t.Errorf("etag = %q, want %q", etag, tc.etag)
			}
		})
	}
}

// TestParseS3URL_Invalid verifies that malformed S3 URLs produce errors.
func TestParseS3URL_Invalid(t *testing.T) {
	cases := []string{
		"s3://",
		"s3:///key",
		"s3://bucket",
		"s3://bucket/",
		"https://bucket/key",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, _, _, _, err := parseS3URL(tc)
			if err == nil {
				t.Errorf("expected error for %q", tc)
			}
		})
	}
}

// TestMergeCustomEvents verifies that user-specified events not already in
// the default set are appended. Duplicate events are not added twice.
func TestMergeCustomEvents(t *testing.T) {
	defaults := []string{"A", "B", "C"}
	result := mergeCustomEvents(defaults, []string{"D", "B", "E"})
	expected := []string{"A", "B", "C", "D", "E"}
	if len(result) != len(expected) {
		t.Fatalf("result = %v, want %v", result, expected)
	}
	for i, e := range result {
		if e != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

// TestValidate_ArchiveTypeButIsDirectory verifies that specifying an archive
// bundle type (tar/tgz/zip) while pointing to a directory path is rejected.
func TestValidate_ArchiveTypeButIsDirectory(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions()
	opts.BundleLocation = dir
	opts.BundleType = "tar"

	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for archive type pointing to a directory")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %v, want mention of directory", err)
	}
}

// TestValidate_DirectoryTypeButIsFile verifies that specifying bundle type
// "directory" while pointing to a regular file is rejected.
func TestValidate_DirectoryTypeButIsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.tar")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.BundleLocation = f
	opts.BundleType = "directory"

	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for directory type pointing to a file")
	}
	if !strings.Contains(err.Error(), "is a file") {
		t.Errorf("error = %v, want mention of file", err)
	}
}

// TestValidate_NonExistentLocalPath verifies that a non-existent local
// bundle path is rejected during validation.
func TestValidate_NonExistentLocalPath(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/nonexistent/path/to/bundle"
	opts.BundleType = "directory"

	err := validate(opts)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

// TestValidate_RemoteLocationSkipsFileCheck verifies that S3 URLs skip
// the local file existence check. The bundle will be downloaded at runtime.
func TestValidate_RemoteLocationSkipsFileCheck(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "s3://my-bucket/app.tar"
	opts.BundleType = "tar"

	err := validate(opts)
	if err != nil {
		t.Fatalf("validate should pass for remote location: %v", err)
	}
}

// TestParseS3URL_MalformedQueryParam verifies that query params without
// an = sign are silently skipped rather than causing an error.
func TestParseS3URL_MalformedQueryParam(t *testing.T) {
	bucket, key, versionID, etag, err := parseS3URL("s3://bucket/key?badparam&versionId=v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bucket != "bucket" || key != "key" {
		t.Errorf("bucket=%q key=%q", bucket, key)
	}
	if versionID != "v1" {
		t.Errorf("versionID = %q, want v1", versionID)
	}
	if etag != "" {
		t.Errorf("etag = %q, want empty", etag)
	}
}

// TestBuildSpec_CustomEventsInAllPossible verifies that custom events are
// added to AllPossibleLifecycleEvents in the deployment spec.
func TestBuildSpec_CustomEventsInAllPossible(t *testing.T) {
	opts := DefaultOptions()
	opts.BundleLocation = "/opt/myapp"
	opts.BundleType = "directory"
	opts.Events = []string{"CustomEvent"}

	spec, err := buildSpec(opts)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	found := false
	for _, e := range spec.AllPossibleLifecycleEvents {
		if e == "CustomEvent" {
			found = true
			break
		}
	}
	if !found {
		t.Error("CustomEvent should be in AllPossibleLifecycleEvents")
	}
}
