// Package instruction defines install command types and their serialization.
// Commands represent file operations (copy, mkdir, chmod, chown, setfacl, semanage)
// that are generated from the appspec files and permissions sections.
package instruction

import (
	json "github.com/goccy/go-json"
)

// CommandType identifies the kind of install instruction.
type CommandType string

const (
	TypeCopy     CommandType = "copy"
	TypeMkdir    CommandType = "mkdir"
	TypeChmod    CommandType = "chmod"
	TypeChown    CommandType = "chown"
	TypeSetfacl  CommandType = "setfacl"
	TypeSemanage CommandType = "semanage"
)

// Command is a single install instruction. The populated fields depend on Type.
type Command struct {
	Type        CommandType `json:"type"`
	Source      string      `json:"source,omitempty"`
	Destination string      `json:"destination,omitempty"`
	Directory   string      `json:"directory,omitempty"`
	File        string      `json:"file,omitempty"`
	Mode        string      `json:"mode,omitempty"`
	Owner       string      `json:"owner,omitempty"`
	Group       string      `json:"group,omitempty"`
	ACL         []string    `json:"acl,omitempty"`
	Context     *ContextCmd `json:"context,omitempty"`
}

// ContextCmd holds SELinux context fields for semanage commands.
type ContextCmd struct {
	User  string `json:"user,omitempty"`
	Role  string `json:"role,omitempty"`
	Type  string `json:"type"`
	Range string `json:"range,omitempty"`
}

// Instructions wraps a list of commands for JSON serialization.
type Instructions struct {
	Commands []Command `json:"instructions"`
}

// ToJSON serializes the instruction set to JSON format.
//
//	data, err := instructions.ToJSON()
func (inst *Instructions) ToJSON() ([]byte, error) {
	return json.Marshal(inst)
}

// ParseInstallCommands parses a JSON install instructions file.
//
//	inst, err := instruction.ParseInstallCommands(data)
func ParseInstallCommands(data []byte) ([]Command, error) {
	var inst Instructions
	if err := json.Unmarshal(data, &inst); err != nil {
		return nil, err
	}
	return inst.Commands, nil
}

// RemoveEntry represents a single entry in the cleanup file.
// Regular entries are file/directory paths. Semanage entries start with "semanage\0".
type RemoveEntry struct {
	Path      string
	IsContext bool // true if this was a semanage fcontext entry
}

// ParseRemoveCommands parses a cleanup file into reverse-ordered removal entries.
// Incomplete last lines (without trailing newline) are discarded to avoid
// processing partially-written paths from interrupted deployments.
//
//	entries := instruction.ParseRemoveCommands(data)
//	for _, e := range entries { os.Remove(e.Path) }
func ParseRemoveCommands(data string) []RemoveEntry {
	if data == "" {
		return nil
	}

	lines := splitLines(data)

	// Discard last line if it doesn't end with newline (partial write)
	if len(data) > 0 && data[len(data)-1] != '\n' && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	entries := make([]RemoveEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		if len(line) > 9 && line[:9] == "semanage\x00" {
			entries = append(entries, RemoveEntry{
				Path:      line[9:],
				IsContext: true,
			})
		} else {
			entries = append(entries, RemoveEntry{Path: line})
		}
	}

	// Reverse for bottom-up cleanup (files before their parent directories)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
