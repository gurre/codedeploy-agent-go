package appspec

import (
	"fmt"
)

// SELinuxContext represents a parsed SELinux context from appspec permissions.
type SELinuxContext struct {
	User  string
	Role  string
	Type  string
	Range *SELinuxRange
}

// SELinuxRange represents the sensitivity range in an SELinux context.
type SELinuxRange struct {
	Low  string
	High string
}

// GetRange returns the formatted range string for semanage.
//
//	ctx.Range.GetRange() // "s0-s0:c0.c1023" or "s0"
func (r *SELinuxRange) GetRange() string {
	if r.High != "" {
		return fmt.Sprintf("%s-%s", r.Low, r.High)
	}
	return r.Low
}

// ParseContext parses an SELinux context from an appspec permission entry.
//
//	ctx, err := appspec.ParseContext(rawContext{Type: "httpd_sys_content_t"})
func ParseContext(raw rawContext) (SELinuxContext, error) {
	if raw.Type == "" {
		return SELinuxContext{}, fmt.Errorf("appspec: SELinux context must specify a type")
	}

	ctx := SELinuxContext{
		User: raw.User,
		Role: raw.Role,
		Type: raw.Type,
	}

	if raw.Range != nil {
		if raw.Range.Low == "" {
			return SELinuxContext{}, fmt.Errorf("appspec: SELinux range must specify a low value")
		}
		ctx.Range = &SELinuxRange{
			Low:  raw.Range.Low,
			High: raw.Range.High,
		}
	}

	return ctx, nil
}
