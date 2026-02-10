package appspec

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Permission represents a parsed permission entry from the appspec permissions section.
type Permission struct {
	Object  string
	Pattern string // glob pattern, default "**"
	Except  []string
	Type    []string // "file", "directory", or both
	Owner   string
	Group   string
	Mode    *Mode
	ACLs    *ACL
	Context *SELinuxContext
}

// MatchesPattern checks whether a path matches this permission's object + pattern.
// The path is checked relative to the permission's object base directory.
//
//	perm := Permission{Object: "/opt/app", Pattern: "**"}
//	perm.MatchesPattern("/opt/app/config/db.yml") // true
func (p *Permission) MatchesPattern(path string) bool {
	path = strings.TrimSuffix(path, string(filepath.Separator))
	base := filepath.Clean(p.Object)
	if !strings.HasSuffix(base, string(filepath.Separator)) {
		base += string(filepath.Separator)
	}

	if !strings.HasPrefix(path, base) {
		return false
	}

	if p.Pattern == "**" {
		return true
	}

	relName := path[len(base):]
	return matchSimpleGlob(relName, p.Pattern)
}

// MatchesExcept checks whether a path matches any of the exception patterns.
//
//	perm := Permission{Object: "/opt/app", Except: []string{"*.log"}}
//	perm.MatchesExcept("/opt/app/debug.log") // true
func (p *Permission) MatchesExcept(path string) bool {
	path = strings.TrimSuffix(path, string(filepath.Separator))
	base := filepath.Clean(p.Object)
	if !strings.HasSuffix(base, string(filepath.Separator)) {
		base += string(filepath.Separator)
	}

	if !strings.HasPrefix(path, base) {
		return false
	}

	relName := path[len(base):]
	for _, exc := range p.Except {
		if matchSimpleGlob(relName, exc) {
			return true
		}
	}
	return false
}

// ValidateFilePermission checks that file-type permissions don't use patterns
// or except lists (which only make sense for directories).
func (p *Permission) ValidateFilePermission() error {
	if hasType(p.Type, "file") {
		if p.Pattern != "**" {
			return fmt.Errorf("appspec: file permission for %q has invalid pattern %q", p.Object, p.Pattern)
		}
		if len(p.Except) > 0 {
			return fmt.Errorf("appspec: file permission for %q cannot have except list", p.Object)
		}
	}
	return nil
}

// ValidateFileACL ensures default ACL entries are not applied to files.
func (p *Permission) ValidateFileACL() error {
	if p.ACLs != nil && p.ACLs.GetDefaultACE() != "" {
		return fmt.Errorf("appspec: default ACL cannot be applied to a file")
	}
	return nil
}

func hasType(types []string, target string) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}

// matchSimpleGlob implements the Ruby agent's simple glob matching.
// It does NOT match across path separators.
func matchSimpleGlob(name, pattern string) bool {
	if strings.Contains(name, string(filepath.Separator)) {
		return false
	}
	options := expand([]rune(pattern))
	for _, ch := range name {
		var newOptions [][]rune
		for _, opt := range options {
			if len(opt) == 0 {
				continue
			}
			if opt[0] == '*' {
				newOptions = append(newOptions, expand(opt)...)
			} else if opt[0] == rune(ch) {
				newOptions = append(newOptions, expand(opt[1:])...)
			}
		}
		options = newOptions
		for _, opt := range options {
			if len(opt) == 1 && opt[0] == '*' {
				return true
			}
		}
	}
	for _, opt := range options {
		if len(opt) == 0 {
			return true
		}
	}
	return false
}

// expand consumes leading '*' characters, producing both the starred and
// un-starred continuations (mirroring the Ruby expand method).
func expand(option []rune) [][]rune {
	var prev []rune
	for len(option) > 0 && option[0] == '*' {
		prev = make([]rune, len(option))
		copy(prev, option)
		option = option[1:]
	}
	if prev == nil {
		return [][]rune{option}
	}
	return [][]rune{prev, option}
}
