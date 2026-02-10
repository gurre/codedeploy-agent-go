package deployspec

import (
	"testing"
)

// stubVerifier implements CertificateVerifier for testing, returning the
// payload unchanged. This isolates spec parsing from PKCS7 crypto.
type stubVerifier struct{}

func (s *stubVerifier) Verify(sig []byte) ([]byte, error) {
	return sig, nil
}

// TestParseS3Spec verifies parsing of a complete S3-sourced deployment spec.
// This is the most common deployment type in production.
func TestParseS3Spec(t *testing.T) {
	payload := `{
		"DeploymentId": "d-ABCDEF123",
		"DeploymentGroupId": "dg-123",
		"DeploymentGroupName": "prod",
		"ApplicationName": "MyApp",
		"Revision": {
			"RevisionType": "S3",
			"S3Revision": {
				"Bucket": "my-bucket",
				"Key": "app.tar",
				"BundleType": "tar",
				"Version": "v1",
				"ETag": "abc123"
			}
		}
	}`

	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.DeploymentID != "d-ABCDEF123" {
		t.Errorf("DeploymentID = %q", spec.DeploymentID)
	}
	if spec.Source != RevisionS3 {
		t.Errorf("Source = %q", spec.Source)
	}
	if spec.Bucket != "my-bucket" {
		t.Errorf("Bucket = %q", spec.Bucket)
	}
	if spec.Key != "app.tar" {
		t.Errorf("Key = %q", spec.Key)
	}
	if spec.ETag != "abc123" {
		t.Errorf("ETag = %q", spec.ETag)
	}
}

// TestParseGitHubSpec verifies parsing of a GitHub-sourced deployment spec.
func TestParseGitHubSpec(t *testing.T) {
	payload := `{
		"DeploymentId": "d-GH123",
		"DeploymentGroupId": "dg-456",
		"DeploymentGroupName": "staging",
		"ApplicationName": "WebApp",
		"Revision": {
			"RevisionType": "GitHub",
			"GitHubRevision": {
				"Account": "octocat",
				"Repository": "hello-world",
				"CommitId": "abc123"
			}
		},
		"GitHubAccessToken": "ghp_secret"
	}`

	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Source != RevisionGitHub {
		t.Errorf("Source = %q", spec.Source)
	}
	if spec.Account != "octocat" {
		t.Errorf("Account = %q", spec.Account)
	}
	if spec.Anonymous {
		t.Error("should not be anonymous with token")
	}
}

// TestParseGitHubAnonymous verifies that missing token sets Anonymous=true.
func TestParseGitHubAnonymous(t *testing.T) {
	payload := `{
		"DeploymentId": "d-GH456",
		"DeploymentGroupId": "dg-789",
		"DeploymentGroupName": "dev",
		"ApplicationName": "OpenSource",
		"Revision": {
			"RevisionType": "GitHub",
			"GitHubRevision": {
				"Account": "pub",
				"Repository": "repo",
				"CommitId": "def456"
			}
		}
	}`

	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.Anonymous {
		t.Error("should be anonymous without token")
	}
}

// TestParseLocalSpec verifies parsing of a local file deployment spec, used
// by the codedeploy-local CLI tool.
func TestParseLocalSpec(t *testing.T) {
	payload := `{
		"DeploymentId": "d-LOCAL1",
		"DeploymentGroupId": "dg-local",
		"DeploymentGroupName": "local",
		"ApplicationName": "LocalApp",
		"Revision": {
			"RevisionType": "Local File",
			"LocalRevision": {
				"Location": "/tmp/bundle.tar",
				"BundleType": "tar"
			}
		}
	}`

	spec, err := Parse(Envelope{Format: "TEXT/JSON", Payload: payload}, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Source != RevisionLocalFile {
		t.Errorf("Source = %q", spec.Source)
	}
	if spec.LocalLocation != "/tmp/bundle.tar" {
		t.Errorf("LocalLocation = %q", spec.LocalLocation)
	}
}

// TestParseTextJSONRejectedWithoutFlag ensures TEXT/JSON is blocked unless the
// allowUnsigned flag is set. This prevents accepting unsigned specs in production.
func TestParseTextJSONRejectedWithoutFlag(t *testing.T) {
	payload := `{"DeploymentId":"d-1","DeploymentGroupId":"dg-1","DeploymentGroupName":"n","ApplicationName":"a","Revision":{"RevisionType":"S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}}`
	_, err := Parse(Envelope{Format: "TEXT/JSON", Payload: payload}, nil, false)
	if err == nil {
		t.Fatal("expected error for TEXT/JSON without allowUnsigned")
	}
}

// TestExtractDeploymentIDFromARN verifies ARN-to-ID extraction. The CodeDeploy
// service returns ARN-format IDs that must be converted to the short form.
func TestExtractDeploymentIDFromARN(t *testing.T) {
	arn := "arn:aws:codedeploy:us-east-1:123412341234:deployment/d-ABCDEF123"
	got := extractDeploymentID(arn)
	want := "d-ABCDEF123"
	if got != want {
		t.Errorf("extractDeploymentID(%q) = %q, want %q", arn, got, want)
	}
}

// TestExtractDeploymentIDPlain verifies that plain IDs pass through unchanged.
func TestExtractDeploymentIDPlain(t *testing.T) {
	id := "d-ABCDEF123"
	got := extractDeploymentID(id)
	if got != id {
		t.Errorf("extractDeploymentID(%q) = %q, want %q", id, got, id)
	}
}

// TestParseDefaultValues verifies that DeploymentCreator, DeploymentType, and
// AppSpecPath default to "user", "IN_PLACE", and "appspec.yml" respectively
// when not provided in the spec.
func TestParseDefaultValues(t *testing.T) {
	payload := `{
		"DeploymentId": "d-1",
		"DeploymentGroupId": "dg-1",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {
			"RevisionType": "S3",
			"S3Revision": {"Bucket":"b","Key":"k","BundleType":"tar"}
		}
	}`

	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.DeploymentCreator != "user" {
		t.Errorf("DeploymentCreator = %q, want user", spec.DeploymentCreator)
	}
	if spec.DeploymentType != "IN_PLACE" {
		t.Errorf("DeploymentType = %q, want IN_PLACE", spec.DeploymentType)
	}
	if spec.AppSpecPath != "appspec.yml" {
		t.Errorf("AppSpecPath = %q, want appspec.yml", spec.AppSpecPath)
	}
	if spec.FileExistsBehavior != "DISALLOW" {
		t.Errorf("FileExistsBehavior = %q, want DISALLOW", spec.FileExistsBehavior)
	}
}

// TestParseFileExistsBehaviorOverride verifies that AgentActionOverrides
// correctly overrides the default DISALLOW behavior.
func TestParseFileExistsBehaviorOverride(t *testing.T) {
	payload := `{
		"DeploymentId": "d-2",
		"DeploymentGroupId": "dg-2",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {
			"RevisionType": "S3",
			"S3Revision": {"Bucket":"b","Key":"k","BundleType":"tar"}
		},
		"AgentActionOverrides": {
			"AgentOverrides": {
				"FileExistsBehavior": "overwrite"
			}
		}
	}`

	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.FileExistsBehavior != "OVERWRITE" {
		t.Errorf("FileExistsBehavior = %q, want OVERWRITE", spec.FileExistsBehavior)
	}
}

// TestParseMissingRequiredFields tests that each required field produces an error.
func TestParseMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"missing DeploymentId", `{"DeploymentGroupId":"dg","DeploymentGroupName":"n","ApplicationName":"a","Revision":{"RevisionType":"S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}}`},
		{"missing DeploymentGroupId", `{"DeploymentId":"d","DeploymentGroupName":"n","ApplicationName":"a","Revision":{"RevisionType":"S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}}`},
		{"missing ApplicationName", `{"DeploymentId":"d","DeploymentGroupId":"dg","DeploymentGroupName":"n","Revision":{"RevisionType":"S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}}`},
	}
	for _, tc := range cases {
		_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: tc.payload}, &stubVerifier{}, false)
		if err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

// TestParseInvalidS3BundleType rejects non-standard bundle types for S3 revisions.
func TestParseInvalidS3BundleType(t *testing.T) {
	payload := `{
		"DeploymentId": "d-3",
		"DeploymentGroupId": "dg-3",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {
			"RevisionType": "S3",
			"S3Revision": {"Bucket":"b","Key":"k","BundleType":"rar"}
		}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for invalid S3 BundleType")
	}
}

// TestParseEmptyEnvelope verifies that an empty envelope (no format, no payload)
// returns an error. This protects against nil/zero-value spec objects propagating.
func TestParseEmptyEnvelope(t *testing.T) {
	_, err := Parse(Envelope{}, nil, false)
	if err == nil {
		t.Fatal("expected error for empty envelope")
	}
}

// TestParseUnsupportedFormat verifies that an unknown format string is rejected.
// This guards against silently accepting new formats without verification logic.
func TestParseUnsupportedFormat(t *testing.T) {
	_, err := Parse(Envelope{Format: "XML/SIGNED", Payload: "<xml/>"}, nil, false)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

// TestParsePKCS7WithNilVerifier verifies that PKCS7/JSON without a verifier
// returns an error. The verifier is mandatory for signed payloads.
func TestParsePKCS7WithNilVerifier(t *testing.T) {
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: "{}"}, nil, false)
	if err == nil {
		t.Fatal("expected error for nil verifier with PKCS7")
	}
}

// TestParseMissingRevisionType verifies that a spec with no RevisionType returns
// an error. The revision source determines the entire download path.
func TestParseMissingRevisionType(t *testing.T) {
	payload := `{
		"DeploymentId": "d-4",
		"DeploymentGroupId": "dg-4",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for missing revision type")
	}
}

// TestParseUnsupportedRevisionType verifies that an unknown revision type is rejected.
func TestParseUnsupportedRevisionType(t *testing.T) {
	payload := `{
		"DeploymentId": "d-5",
		"DeploymentGroupId": "dg-5",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "FTP"}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for unsupported revision type")
	}
}

// TestParseInvalidJSON verifies that malformed JSON in the payload is rejected.
func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: "{not-json"}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestParseMissingDeploymentGroupName verifies that a missing DeploymentGroupName
// is caught by validation. All four required fields must be present.
func TestParseMissingDeploymentGroupName(t *testing.T) {
	payload := `{
		"DeploymentId": "d-6",
		"DeploymentGroupId": "dg-6",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for missing DeploymentGroupName")
	}
}

// TestParseS3MissingBucket verifies that an S3 revision without a bucket is rejected.
func TestParseS3MissingBucket(t *testing.T) {
	payload := `{
		"DeploymentId": "d-7",
		"DeploymentGroupId": "dg-7",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "S3","S3Revision":{"Key":"k","BundleType":"tar"}}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for S3 missing Bucket")
	}
}

// TestParseGitHubMissingCommitID verifies that a GitHub revision without CommitId
// is rejected.
func TestParseGitHubMissingCommitID(t *testing.T) {
	payload := `{
		"DeploymentId": "d-8",
		"DeploymentGroupId": "dg-8",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "GitHub","GitHubRevision":{"Account":"a","Repository":"r"}}
	}`
	_, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err == nil {
		t.Fatal("expected error for GitHub missing CommitId")
	}
}

// TestParseLocalDirectorySpec verifies that Local Directory revisions are
// correctly distinguished from Local File revisions.
func TestParseLocalDirectorySpec(t *testing.T) {
	payload := `{
		"DeploymentId": "d-LOCAL2",
		"DeploymentGroupId": "dg-local2",
		"DeploymentGroupName": "local",
		"ApplicationName": "DirApp",
		"Revision": {
			"RevisionType": "Local Directory",
			"LocalRevision": {"Location": "/opt/deploy/src", "BundleType": "directory"}
		}
	}`
	spec, err := Parse(Envelope{Format: "TEXT/JSON", Payload: payload}, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Source != RevisionLocalDirectory {
		t.Errorf("Source = %q, want %q", spec.Source, RevisionLocalDirectory)
	}
	if spec.BundleType != "directory" {
		t.Errorf("BundleType = %q, want directory", spec.BundleType)
	}
}

// TestParseLocalInvalidBundleType verifies that invalid local bundle types are rejected.
func TestParseLocalInvalidBundleType(t *testing.T) {
	payload := `{
		"DeploymentId": "d-LOCAL3",
		"DeploymentGroupId": "dg-local3",
		"DeploymentGroupName": "local",
		"ApplicationName": "BadApp",
		"Revision": {
			"RevisionType": "Local File",
			"LocalRevision": {"Location": "/tmp/bundle.rar", "BundleType": "rar"}
		}
	}`
	_, err := Parse(Envelope{Format: "TEXT/JSON", Payload: payload}, nil, true)
	if err == nil {
		t.Fatal("expected error for invalid local BundleType")
	}
}

// TestParseLocalMissingLocation verifies that a local revision without Location
// is rejected.
func TestParseLocalMissingLocation(t *testing.T) {
	payload := `{
		"DeploymentId": "d-LOCAL4",
		"DeploymentGroupId": "dg-local4",
		"DeploymentGroupName": "local",
		"ApplicationName": "NoLoc",
		"Revision": {
			"RevisionType": "Local File",
			"LocalRevision": {"BundleType": "tar"}
		}
	}`
	_, err := Parse(Envelope{Format: "TEXT/JSON", Payload: payload}, nil, true)
	if err == nil {
		t.Fatal("expected error for local missing Location")
	}
}

// TestParseDeploymentIDFromARN verifies the ARN extraction within a full Parse flow.
func TestParseDeploymentIDFromARN(t *testing.T) {
	payload := `{
		"DeploymentId": "arn:aws:codedeploy:us-east-1:123:deployment/d-FROM-ARN",
		"DeploymentGroupId": "dg-9",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}}
	}`
	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.DeploymentID != "d-FROM-ARN" {
		t.Errorf("DeploymentID = %q, want d-FROM-ARN", spec.DeploymentID)
	}
}

// TestParseAllPossibleLifecycleEvents verifies that the event list is preserved.
func TestParseAllPossibleLifecycleEvents(t *testing.T) {
	payload := `{
		"DeploymentId": "d-10",
		"DeploymentGroupId": "dg-10",
		"DeploymentGroupName": "grp",
		"ApplicationName": "app",
		"Revision": {"RevisionType": "S3","S3Revision":{"Bucket":"b","Key":"k","BundleType":"tar"}},
		"AllPossibleLifecycleEvents": ["BeforeInstall", "AfterInstall"]
	}`
	spec, err := Parse(Envelope{Format: "PKCS7/JSON", Payload: payload}, &stubVerifier{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.AllPossibleLifecycleEvents) != 2 {
		t.Errorf("AllPossibleLifecycleEvents length = %d, want 2", len(spec.AllPossibleLifecycleEvents))
	}
}
