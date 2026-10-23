package cloudinit

import (
	"fmt"
	"strings"
)

// allowedMountOptions is the closed set of ext4/mount(8) options that may be
// rendered into the autoinstall storage mount entries. Exact tokens only, so
// arbitrary strings can never reach the generated YAML.
var allowedMountOptions = map[string]bool{
	"defaults": true, "nosuid": true, "nodev": true, "noexec": true,
	"noatime": true, "relatime": true, "nodiratime": true, "lazytime": true,
	"usrquota": true, "grpquota": true, "prjquota": true,
	"usrjquota=aquota.user": true, "grpjquota=aquota.group": true,
	"jqfmt=vfsold": true, "jqfmt=vfsv0": true, "jqfmt=vfsv1": true,
	"data=ordered": true, "data=writeback": true, "data=journal": true,
	"errors=remount-ro": true, "errors=continue": true, "errors=panic": true,
	"acl": true, "noacl": true, "user_xattr": true, "nouser_xattr": true,
	"discard": true, "nodiscard": true,
}

// ValidateMountOptions rejects any option not in the allow-list.
func ValidateMountOptions(field string, opts []string) error {
	for _, o := range opts {
		if !allowedMountOptions[o] {
			return fmt.Errorf("%s: mount option %q is not allowed", field, o)
		}
	}
	return nil
}

// joinMountOptions renders the curtin `options` value. Curtin's options key
// REPLACES the default "defaults", so it is always kept first.
// Empty input returns "" (the template then omits the key).
func joinMountOptions(opts []string) string {
	if len(opts) == 0 {
		return ""
	}
	out := []string{"defaults"}
	for _, o := range opts {
		if o != "defaults" {
			out = append(out, o)
		}
	}
	return strings.Join(out, ",")
}
