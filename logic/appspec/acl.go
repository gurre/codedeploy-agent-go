package appspec

import (
	"fmt"
	"strings"
)

// ACL represents parsed Access Control List entries from appspec permissions.
type ACL struct {
	Entries []string
}

// ParseACL parses ACL entries from the appspec permissions section.
// Each entry follows POSIX ACL format: [default:]<tag>:<qualifier>:<perms>
//
//	acl, err := appspec.ParseACL([]string{"user:deploy:rwx", "group:web:r-x"})
func ParseACL(entries []string) (ACL, error) {
	parsed := make([]string, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if err := validateACE(e); err != nil {
			return ACL{}, err
		}
		parsed = append(parsed, e)
	}
	return ACL{Entries: parsed}, nil
}

// GetACL returns the list of ACL entries.
func (a *ACL) GetACL() []string {
	return a.Entries
}

// HasDefault returns whether any entry starts with "d:" or "default:".
func (a *ACL) HasDefault() bool {
	for _, e := range a.Entries {
		if strings.HasPrefix(e, "d:") || strings.HasPrefix(e, "default:") {
			return true
		}
	}
	return false
}

// HasBaseNamed returns whether any non-default entry specifies a named user or group.
func (a *ACL) HasBaseNamed() bool {
	for _, e := range a.Entries {
		if strings.HasPrefix(e, "d:") || strings.HasPrefix(e, "default:") {
			continue
		}
		parts := strings.SplitN(e, ":", 3)
		if len(parts) >= 3 {
			tag := parts[0]
			qualifier := parts[1]
			if (tag == "user" || tag == "u" || tag == "group" || tag == "g") && qualifier != "" {
				return true
			}
		}
	}
	return false
}

// HasBaseMask returns whether a non-default mask entry exists.
func (a *ACL) HasBaseMask() bool {
	for _, e := range a.Entries {
		if strings.HasPrefix(e, "d:") || strings.HasPrefix(e, "default:") {
			continue
		}
		if strings.HasPrefix(e, "m:") || strings.HasPrefix(e, "mask:") {
			return true
		}
	}
	return false
}

// HasDefaultUser returns whether a default user entry exists.
func (a *ACL) HasDefaultUser() bool {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "u:") || strings.HasPrefix(stripped, "user:") {
			parts := strings.SplitN(stripped, ":", 3)
			if len(parts) >= 2 && parts[1] == "" {
				return true
			}
		}
	}
	return false
}

// HasDefaultGroup returns whether a default group entry exists.
func (a *ACL) HasDefaultGroup() bool {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "g:") || strings.HasPrefix(stripped, "group:") {
			parts := strings.SplitN(stripped, ":", 3)
			if len(parts) >= 2 && parts[1] == "" {
				return true
			}
		}
	}
	return false
}

// HasDefaultOther returns whether a default other entry exists.
func (a *ACL) HasDefaultOther() bool {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "o:") || strings.HasPrefix(stripped, "other:") {
			return true
		}
	}
	return false
}

// HasDefaultNamed returns whether any default entry specifies a named user or group.
func (a *ACL) HasDefaultNamed() bool {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		parts := strings.SplitN(stripped, ":", 3)
		if len(parts) >= 3 {
			tag := parts[0]
			qualifier := parts[1]
			if (tag == "user" || tag == "u" || tag == "group" || tag == "g") && qualifier != "" {
				return true
			}
		}
	}
	return false
}

// HasDefaultMask returns whether a default mask entry exists.
func (a *ACL) HasDefaultMask() bool {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "m:") || strings.HasPrefix(stripped, "mask:") {
			return true
		}
	}
	return false
}

// GetDefaultACE returns the first default ACL entry, or empty string.
func (a *ACL) GetDefaultACE() string {
	for _, e := range a.Entries {
		if strings.HasPrefix(e, "d:") || strings.HasPrefix(e, "default:") {
			return e
		}
	}
	return ""
}

// GetDefaultGroupACE returns the first default group ACL entry, or empty string.
func (a *ACL) GetDefaultGroupACE() string {
	for _, e := range a.Entries {
		stripped := stripDefault(e)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "g:") || strings.HasPrefix(stripped, "group:") {
			return e
		}
	}
	return ""
}

// AddEntry adds an ACL entry to the list.
func (a *ACL) AddEntry(entry string) {
	a.Entries = append(a.Entries, entry)
}

// ClearAdditional removes any entries that were dynamically added beyond the original set.
// This is used after setfacl execution to restore the original state.
func (a *ACL) ClearAdditional(originalCount int) {
	if originalCount < len(a.Entries) {
		a.Entries = a.Entries[:originalCount]
	}
}

func stripDefault(e string) string {
	if strings.HasPrefix(e, "d:") {
		return e[2:]
	}
	if strings.HasPrefix(e, "default:") {
		return e[8:]
	}
	return ""
}

func validateACE(entry string) error {
	// Basic validation: entries should have at least tag:perms format
	parts := strings.Split(entry, ":")
	if len(parts) < 2 {
		return fmt.Errorf("appspec: invalid ACL entry %q", entry)
	}
	return nil
}
