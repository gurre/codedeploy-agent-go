package deployspec

import "testing"

// stubVerifier returns fixed data without doing real PKCS7 work.
type benchVerifier struct {
	data []byte
}

func (v *benchVerifier) Verify(_ []byte) ([]byte, error) {
	return v.data, nil
}

// BenchmarkParse measures deployment spec JSON parsing throughput.
// This runs once per deployment command when the spec envelope is received.
func BenchmarkParse(b *testing.B) {
	payload := `{
		"DeploymentId": "d-ABCDEF123",
		"DeploymentGroupId": "group-1",
		"DeploymentGroupName": "production",
		"ApplicationName": "my-app",
		"DeploymentCreator": "user",
		"DeploymentType": "IN_PLACE",
		"AppSpecFilename": "appspec.yml",
		"Revision": {
			"RevisionType": "S3",
			"S3Revision": {
				"Bucket": "my-bucket",
				"Key": "releases/v1.0.tar",
				"BundleType": "tar",
				"Version": "abc123",
				"ETag": "def456"
			}
		}
	}`
	v := &benchVerifier{data: []byte(payload)}
	env := Envelope{Format: "PKCS7/JSON", Payload: "signed-data"}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := Parse(env, v, false)
		if err != nil {
			b.Fatal(err)
		}
	}
}
