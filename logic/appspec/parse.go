// Package appspec parses AWS CodeDeploy application specification (appspec.yml) files.
// It handles files, hooks, and permissions sections with full validation.
package appspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec represents a parsed appspec.yml with all sections validated.
type Spec struct {
	Version            float64
	OS                 string
	Hooks              map[string][]Script
	Files              []FileMapping
	Permissions        []Permission
	FileExistsBehavior string
}

// Script holds one hook script entry from the appspec hooks section.
type Script struct {
	Location string
	RunAs    string
	Timeout  int // seconds, default 3600
	Sudo     bool
}

// FileMapping holds one source→destination entry from the appspec files section.
type FileMapping struct {
	Source      string
	Destination string
}

// rawSpec mirrors the YAML structure for unmarshalling.
type rawSpec struct {
	Version            interface{} `yaml:"version"`
	OS                 string      `yaml:"os"`
	Hooks              yaml.Node   `yaml:"hooks"`
	Files              []rawFile   `yaml:"files"`
	Permissions        []rawPerm   `yaml:"permissions"`
	FileExistsBehavior string      `yaml:"file_exists_behavior"`
}

type rawFile struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
}

type rawScript struct {
	Location string      `yaml:"location"`
	RunAs    string      `yaml:"runas"`
	Sudo     bool        `yaml:"sudo"`
	Timeout  interface{} `yaml:"timeout"`
}

type rawPerm struct {
	Object  string      `yaml:"object"`
	Pattern string      `yaml:"pattern"`
	Except  []string    `yaml:"except"`
	Type    []string    `yaml:"type"`
	Owner   string      `yaml:"owner"`
	Group   string      `yaml:"group"`
	Mode    interface{} `yaml:"mode"`
	ACLs    *rawACL     `yaml:"acls"`
	Context *rawContext `yaml:"context"`
}

type rawACL struct {
	Entries []string `yaml:",flow"`
}

type rawContext struct {
	User  string    `yaml:"user"`
	Role  string    `yaml:"role"`
	Type  string    `yaml:"type"`
	Range *rawRange `yaml:"range"`
}

type rawRange struct {
	Low  string `yaml:"low"`
	High string `yaml:"high"`
}

var (
	supportedVersions        = []float64{0.0}
	supportedOSes            = []string{"linux", "windows"}
	validFileExistsBehaviors = []string{"DISALLOW", "OVERWRITE", "RETAIN"}
	supportedTypes           = []string{"file", "directory"}
)

// Parse parses an appspec.yml from raw YAML bytes and validates all sections.
//
//	spec, err := appspec.Parse(data)
//	for hook, scripts := range spec.Hooks { ... }
func Parse(data []byte) (Spec, error) {
	var raw rawSpec
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Spec{}, fmt.Errorf("appspec: YAML parse error: %w", err)
	}

	spec := Spec{
		Hooks: make(map[string][]Script),
	}

	version, err := parseVersion(raw.Version)
	if err != nil {
		return Spec{}, err
	}
	spec.Version = version

	spec.OS, err = parseOS(raw.OS)
	if err != nil {
		return Spec{}, err
	}

	spec.Hooks, err = parseHooks(raw.Hooks)
	if err != nil {
		return Spec{}, err
	}

	spec.Files, err = parseFiles(raw.Files)
	if err != nil {
		return Spec{}, err
	}

	spec.Permissions, err = parsePermissions(raw.Permissions, spec.OS)
	if err != nil {
		return Spec{}, err
	}

	spec.FileExistsBehavior, err = parseFileExistsBehavior(raw.FileExistsBehavior)
	if err != nil {
		return Spec{}, err
	}

	return spec, nil
}

// ParseFile reads and parses an appspec file from disk.
//
//	spec, err := appspec.ParseFile("/opt/deployment/appspec.yml")
func ParseFile(path string) (Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("appspec: %w", err)
	}
	return Parse(data)
}

// FindAppSpecFile locates the appspec file in a deployment archive directory.
// When a custom filename is specified (not appspec.yml or appspec.yaml), only
// that exact file is tried — no fallback. For default names, it tries the given
// path first, then appspec.yaml, then appspec.yml.
//
//	path, err := appspec.FindAppSpecFile("/opt/deploy/archive", "appspec.yml")
//	path, err := appspec.FindAppSpecFile("/opt/deploy/archive", "custom.yml") // no fallback
func FindAppSpecFile(archiveDir, appSpecPath string) (string, error) {
	// Custom filename: no fallback to defaults
	if appSpecPath != "appspec.yml" && appSpecPath != "appspec.yaml" {
		exact := filepath.Join(archiveDir, appSpecPath)
		if _, err := os.Stat(exact); err != nil {
			return "", fmt.Errorf("appspec: custom appspec file %q not found in %q", appSpecPath, archiveDir)
		}
		return exact, nil
	}

	candidates := []string{
		filepath.Join(archiveDir, appSpecPath),
	}
	if appSpecPath != "appspec.yaml" {
		candidates = append(candidates, filepath.Join(archiveDir, "appspec.yaml"))
	}
	if appSpecPath != "appspec.yml" {
		candidates = append(candidates, filepath.Join(archiveDir, "appspec.yml"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("appspec: no appspec file found in %q (tried %s)", archiveDir, appSpecPath)
}

func parseVersion(v interface{}) (float64, error) {
	var version float64
	switch val := v.(type) {
	case float64:
		version = val
	case int:
		version = float64(val)
	case nil:
		return 0, fmt.Errorf("appspec: version is required")
	default:
		return 0, fmt.Errorf("appspec: invalid version value (%v)", v)
	}

	for _, sv := range supportedVersions {
		if version == sv {
			return version, nil
		}
	}
	return 0, fmt.Errorf("appspec: invalid version value (%v), must be 0.0", v)
}

func parseOS(osVal string) (string, error) {
	for _, s := range supportedOSes {
		if osVal == s {
			return osVal, nil
		}
	}
	return "", fmt.Errorf("appspec: unsupported operating system (%q), must be linux or windows", osVal)
}

func parseHooks(node yaml.Node) (map[string][]Script, error) {
	hooks := make(map[string][]Script)
	if node.Kind == 0 {
		return hooks, nil
	}

	// The hooks node is a mapping
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("appspec: hooks must be a mapping")
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		hookName := node.Content[i].Value
		scriptsNode := node.Content[i+1]

		// Skip null/empty hooks
		if scriptsNode.Kind == yaml.ScalarNode && scriptsNode.Tag == "!!null" {
			continue
		}
		if scriptsNode.Kind != yaml.SequenceNode {
			continue
		}

		var scripts []Script
		for _, scriptNode := range scriptsNode.Content {
			var rs rawScript
			if err := scriptNode.Decode(&rs); err != nil {
				return nil, fmt.Errorf("appspec: error parsing hook %q: %w", hookName, err)
			}

			loc := strings.TrimSpace(rs.Location)
			if loc == "" {
				return nil, fmt.Errorf("appspec: hook %q has a script with no location", hookName)
			}

			timeout := 3600
			if rs.Timeout != nil {
				switch tv := rs.Timeout.(type) {
				case int:
					timeout = tv
				case float64:
					timeout = int(tv)
				default:
					return nil, fmt.Errorf("appspec: invalid timeout value in hook %q", hookName)
				}
			}
			if timeout <= 0 {
				return nil, fmt.Errorf("appspec: invalid timeout value (%d) in hook %q", timeout, hookName)
			}

			scripts = append(scripts, Script{
				Location: loc,
				RunAs:    strings.TrimSpace(rs.RunAs),
				Sudo:     rs.Sudo,
				Timeout:  timeout,
			})
		}

		if len(scripts) > 0 {
			hooks[hookName] = scripts
		}
	}

	return hooks, nil
}

func parseFiles(raw []rawFile) ([]FileMapping, error) {
	files := make([]FileMapping, 0, len(raw))
	for _, f := range raw {
		if f.Source == "" {
			return nil, fmt.Errorf("appspec: file entry missing source")
		}
		if f.Destination == "" {
			return nil, fmt.Errorf("appspec: file entry for source %q missing destination", f.Source)
		}
		files = append(files, FileMapping(f))
	}
	return files, nil
}

func parsePermissions(raw []rawPerm, osTarget string) ([]Permission, error) {
	perms := make([]Permission, 0, len(raw))
	for _, rp := range raw {
		if strings.TrimSpace(rp.Object) == "" {
			return nil, fmt.Errorf("appspec: permission entry missing object")
		}
		if osTarget != "linux" {
			return nil, fmt.Errorf("appspec: permissions are only supported on linux")
		}

		pattern := rp.Pattern
		if pattern == "" || pattern == "**" {
			pattern = "**"
		}

		types := rp.Type
		if len(types) == 0 {
			types = []string{"file", "directory"}
		}
		for _, t := range types {
			valid := false
			for _, st := range supportedTypes {
				if t == st {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("appspec: unsupported permission type %q", t)
			}
		}

		perm := Permission{
			Object:  strings.TrimSpace(rp.Object),
			Pattern: pattern,
			Except:  rp.Except,
			Type:    types,
			Owner:   rp.Owner,
			Group:   rp.Group,
		}

		if rp.Mode != nil {
			mode, err := ParseMode(rp.Mode)
			if err != nil {
				return nil, err
			}
			perm.Mode = &mode
		}

		if rp.ACLs != nil {
			acl, err := ParseACL(rp.ACLs.Entries)
			if err != nil {
				return nil, err
			}
			perm.ACLs = &acl
		}

		if rp.Context != nil {
			ctx, err := ParseContext(*rp.Context)
			if err != nil {
				return nil, err
			}
			perm.Context = &ctx
		}

		perms = append(perms, perm)
	}
	return perms, nil
}

func parseFileExistsBehavior(val string) (string, error) {
	if val == "" {
		return "", nil
	}
	for _, v := range validFileExistsBehaviors {
		if val == v {
			return val, nil
		}
	}
	return "", fmt.Errorf("appspec: invalid file_exists_behavior %q, must be one of DISALLOW,OVERWRITE,RETAIN", val)
}
