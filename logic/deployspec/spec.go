// Package deployspec parses deployment specification envelopes received from
// the CodeDeploy Commands service. It handles both PKCS7/JSON (signed by the
// service) and TEXT/JSON (used by codedeploy-local) formats.
package deployspec

import (
	"fmt"
	"strings"

	json "github.com/goccy/go-json"
)

// CertificateVerifier verifies PKCS7 signatures and returns the signed data.
type CertificateVerifier interface {
	// Verify checks a PKCS7 signature and returns the contained payload.
	Verify(signature []byte) ([]byte, error)
}

// Envelope holds the format and payload of a deployment specification.
type Envelope struct {
	Format  string
	Payload string
}

// RevisionSource identifies where the deployment bundle comes from.
type RevisionSource string

const (
	RevisionS3             RevisionSource = "S3"
	RevisionGitHub         RevisionSource = "GitHub"
	RevisionLocalFile      RevisionSource = "Local File"
	RevisionLocalDirectory RevisionSource = "Local Directory"
)

// Spec holds a fully parsed deployment specification.
type Spec struct {
	DeploymentID        string
	DeploymentGroupID   string
	DeploymentGroupName string
	ApplicationName     string
	DeploymentCreator   string
	DeploymentType      string
	AppSpecPath         string

	// Revision source and type
	Source RevisionSource

	// S3 fields
	Bucket  string
	Key     string
	Version string
	ETag    string

	// GitHub fields
	Account           string
	Repository        string
	CommitID          string
	Anonymous         bool
	ExternalAuthToken string

	// Local fields
	LocalLocation string

	BundleType string

	FileExistsBehavior         string
	AllPossibleLifecycleEvents []string
}

// rawSpec mirrors the JSON structure for unmarshalling.
type rawSpec struct {
	DeploymentID               string                   `json:"DeploymentId"`
	DeploymentGroupID          string                   `json:"DeploymentGroupId"`
	DeploymentGroupName        string                   `json:"DeploymentGroupName"`
	ApplicationName            string                   `json:"ApplicationName"`
	DeploymentCreator          string                   `json:"DeploymentCreator"`
	DeploymentType             string                   `json:"DeploymentType"`
	AppSpecFilename            string                   `json:"AppSpecFilename"`
	Revision                   rawRevision              `json:"Revision"`
	GitHubAccessToken          string                   `json:"GitHubAccessToken"`
	AgentActionOverrides       *rawAgentActionOverrides `json:"AgentActionOverrides"`
	AllPossibleLifecycleEvents []string                 `json:"AllPossibleLifecycleEvents"`
}

type rawRevision struct {
	RevisionType   string         `json:"RevisionType"`
	S3Revision     *rawS3Revision `json:"S3Revision"`
	GitHubRevision *rawGitHub     `json:"GitHubRevision"`
	LocalRevision  *rawLocal      `json:"LocalRevision"`
}

type rawS3Revision struct {
	Bucket     string `json:"Bucket"`
	Key        string `json:"Key"`
	BundleType string `json:"BundleType"`
	Version    string `json:"Version"`
	ETag       string `json:"ETag"`
}

type rawGitHub struct {
	Account    string `json:"Account"`
	Repository string `json:"Repository"`
	CommitID   string `json:"CommitId"`
	BundleType string `json:"BundleType"`
}

type rawLocal struct {
	Location   string `json:"Location"`
	BundleType string `json:"BundleType"`
}

type rawAgentActionOverrides struct {
	AgentOverrides *rawAgentOverrides `json:"AgentOverrides"`
}

type rawAgentOverrides struct {
	FileExistsBehavior string `json:"FileExistsBehavior"`
}

const defaultFileExistsBehavior = "DISALLOW"

// Parse decodes a deployment specification envelope. For PKCS7/JSON envelopes
// the verifier validates the signature first. For TEXT/JSON (local CLI) the
// allowUnsigned flag must be true.
//
//	spec, err := deployspec.Parse(envelope, verifier, false)
//	fmt.Println(spec.DeploymentID, spec.Source)
func Parse(env Envelope, verifier CertificateVerifier, allowUnsigned bool) (Spec, error) {
	if env.Format == "" && env.Payload == "" {
		return Spec{}, fmt.Errorf("deployspec: envelope is empty")
	}

	var data []byte
	switch env.Format {
	case "PKCS7/JSON":
		if verifier == nil {
			return Spec{}, fmt.Errorf("deployspec: no certificate verifier for PKCS7/JSON")
		}
		var err error
		data, err = verifier.Verify([]byte(env.Payload))
		if err != nil {
			return Spec{}, fmt.Errorf("deployspec: PKCS7 verification failed: %w", err)
		}
	case "TEXT/JSON":
		if !allowUnsigned {
			return Spec{}, fmt.Errorf("deployspec: TEXT/JSON only allowed for local CLI")
		}
		data = []byte(env.Payload)
	default:
		return Spec{}, fmt.Errorf("deployspec: unsupported format %q", env.Format)
	}

	return parseSpecData(data)
}

func parseSpecData(data []byte) (Spec, error) {
	var raw rawSpec
	if err := json.Unmarshal(data, &raw); err != nil {
		return Spec{}, fmt.Errorf("deployspec: JSON parse error: %w", err)
	}

	if raw.DeploymentID == "" {
		return Spec{}, fmt.Errorf("deployspec: missing DeploymentId")
	}
	if raw.DeploymentGroupID == "" {
		return Spec{}, fmt.Errorf("deployspec: missing DeploymentGroupId")
	}
	if raw.DeploymentGroupName == "" {
		return Spec{}, fmt.Errorf("deployspec: missing DeploymentGroupName")
	}
	if raw.ApplicationName == "" {
		return Spec{}, fmt.Errorf("deployspec: missing ApplicationName")
	}

	spec := Spec{
		DeploymentGroupID:          raw.DeploymentGroupID,
		DeploymentGroupName:        raw.DeploymentGroupName,
		ApplicationName:            raw.ApplicationName,
		DeploymentCreator:          orDefault(raw.DeploymentCreator, "user"),
		DeploymentType:             orDefault(raw.DeploymentType, "IN_PLACE"),
		AppSpecPath:                orDefault(raw.AppSpecFilename, "appspec.yml"),
		FileExistsBehavior:         defaultFileExistsBehavior,
		AllPossibleLifecycleEvents: raw.AllPossibleLifecycleEvents,
	}

	// Extract deployment ID from ARN if needed
	spec.DeploymentID = extractDeploymentID(raw.DeploymentID)

	// Parse file_exists_behavior from overrides
	if raw.AgentActionOverrides != nil && raw.AgentActionOverrides.AgentOverrides != nil {
		if feb := raw.AgentActionOverrides.AgentOverrides.FileExistsBehavior; feb != "" {
			spec.FileExistsBehavior = strings.ToUpper(feb)
		}
	}

	if raw.Revision.RevisionType == "" {
		return Spec{}, fmt.Errorf("deployspec: missing revision source")
	}

	spec.Source = RevisionSource(raw.Revision.RevisionType)

	switch spec.Source {
	case RevisionS3:
		r := raw.Revision.S3Revision
		if r == nil || r.Bucket == "" || r.Key == "" || r.BundleType == "" {
			return Spec{}, fmt.Errorf("deployspec: S3 revision must specify Bucket, Key, and BundleType")
		}
		if !validBundleType(r.BundleType) {
			return Spec{}, fmt.Errorf("deployspec: S3 BundleType must be tar, tgz, or zip")
		}
		spec.Bucket = r.Bucket
		spec.Key = r.Key
		spec.BundleType = r.BundleType
		spec.Version = r.Version
		spec.ETag = r.ETag

	case RevisionGitHub:
		r := raw.Revision.GitHubRevision
		if r == nil || r.Account == "" || r.Repository == "" || r.CommitID == "" {
			return Spec{}, fmt.Errorf("deployspec: GitHub revision must specify Account, Repository, and CommitId")
		}
		spec.Account = r.Account
		spec.Repository = r.Repository
		spec.CommitID = r.CommitID
		spec.BundleType = r.BundleType
		spec.ExternalAuthToken = raw.GitHubAccessToken
		spec.Anonymous = raw.GitHubAccessToken == ""

	case RevisionLocalFile, RevisionLocalDirectory:
		r := raw.Revision.LocalRevision
		if r == nil || r.Location == "" || r.BundleType == "" {
			return Spec{}, fmt.Errorf("deployspec: local revision must specify Location and BundleType")
		}
		if !validLocalBundleType(r.BundleType) {
			return Spec{}, fmt.Errorf("deployspec: local BundleType must be tar, tgz, zip, or directory")
		}
		spec.LocalLocation = r.Location
		spec.BundleType = r.BundleType

	default:
		return Spec{}, fmt.Errorf("deployspec: unsupported revision type %q", spec.Source)
	}

	return spec, nil
}

// extractDeploymentID extracts the deployment ID from an ARN or returns as-is.
// ARN format: arn:aws:codedeploy:us-east-1:123412341234:deployment/d-XXXXXXXXX
func extractDeploymentID(id string) string {
	if strings.HasPrefix(id, "arn:") {
		parts := strings.SplitN(id, ":", 6)
		if len(parts) == 6 {
			resourceParts := strings.SplitN(parts[5], "/", 2)
			if len(resourceParts) == 2 {
				return resourceParts[1]
			}
		}
	}
	return id
}

func validBundleType(bt string) bool {
	return bt == "tar" || bt == "tgz" || bt == "zip"
}

func validLocalBundleType(bt string) bool {
	return bt == "tar" || bt == "tgz" || bt == "zip" || bt == "directory"
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
