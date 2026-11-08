// Package kickstart generates Anaconda kickstart files for the alma profile.
package kickstart

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/cloudinit"
)

//go:embed templates/alma.ks.tmpl
var almaTemplate string

var (
	usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	mountRe    = regexp.MustCompile(`^/[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9_./:@+-]+$`)
	fsRe       = regexp.MustCompile(`^(ext4|xfs)$`)
)

// AlmaInput contains the data for ks.cfg generation.
type AlmaInput struct {
	Hostname         string
	Username         string // "root" configures root, anything else creates that user
	PasswordHash     string // crypt(3) hash; empty locks the account password
	SSHPublicKeys    []string
	AllowPasswordSSH bool
	Locale           string
	Timezone         string
	KeyboardLayout   string
	// Device selects the NIC for `network --device`: a MAC address, an interface name or "link".
	Device     string
	IPAddress  string
	Netmask    string
	Gateway    string
	DNS        []string
	Filesystem string // ext4 or xfs
	BootSizeMB int

	DataDiskMountPath    string // empty = no data disk
	RootMountOptions     []string
	DataDiskMountOptions []string
	SwapSizeGB           int // 0 = no swap

	Packages     []string
	UserGroups   string
	BaseOSURL    string
	AppStreamURL string
}

// GenerateAlma renders the kickstart file. Every value that reaches the file is
// validated first so arbitrary strings can never inject kickstart/shell syntax.
func GenerateAlma(in *AlmaInput) (string, error) {
	if err := validate(in); err != nil {
		return "", err
	}
	isRoot := in.Username == "root"
	hash := in.PasswordHash

	rootAuth := "rootpw --lock"
	userAuth := "--lock"
	switch {
	case isRoot && hash != "":
		rootAuth = "rootpw --iscrypted"
		if in.AllowPasswordSSH {
			rootAuth += " --allow-ssh"
		}
		rootAuth += " " + hash
	case !isRoot && hash != "":
		userAuth = "--iscrypted --password=" + hash
	}

	disks := "sda"
	if in.DataDiskMountPath != "" {
		disks = "sda,sdb"
	}
	data := struct {
		*AlmaInput
		User                         bool
		RootAuth, UserAuth           string
		Nameservers, Disks           string
		RootFSOptions, DataFSOptions string
		SwapSizeMB                   int
	}{in, !isRoot, rootAuth, userAuth, strings.Join(in.DNS, ","), disks,
		fsOptions(in.RootMountOptions), fsOptions(in.DataDiskMountOptions), in.SwapSizeGB * 1024}

	tmpl, err := template.New("alma").Parse(almaTemplate)
	if err != nil {
		return "", fmt.Errorf("parse kickstart template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute kickstart template: %w", err)
	}
	return buf.String(), nil
}

// fsOptions renders ` --fsoptions="defaults,..."` (empty input renders nothing).
// Ref: docs/almalinux-official/kickstart-docs.txt (part --fsoptions).
func fsOptions(opts []string) string {
	if len(opts) == 0 {
		return ""
	}
	out := []string{"defaults"}
	for _, o := range opts {
		if o != "defaults" {
			out = append(out, o)
		}
	}
	return fmt.Sprintf(" --fsoptions=%q", strings.Join(out, ","))
}

func validate(in *AlmaInput) error {
	if in.Username != "root" && !usernameRe.MatchString(in.Username) {
		return fmt.Errorf("invalid username %q", in.Username)
	}
	if !hostnameRe.MatchString(in.Hostname) {
		return fmt.Errorf("invalid hostname %q", in.Hostname)
	}
	if !fsRe.MatchString(in.Filesystem) {
		return fmt.Errorf("unsupported filesystem %q (ext4 or xfs)", in.Filesystem)
	}
	if in.BootSizeMB <= 0 {
		return fmt.Errorf("boot size must be positive")
	}
	if in.SwapSizeGB < 0 {
		return fmt.Errorf("swap size must not be negative")
	}
	if in.DataDiskMountPath != "" && !mountRe.MatchString(in.DataDiskMountPath) {
		return fmt.Errorf("invalid data disk mount path %q", in.DataDiskMountPath)
	}
	if len(in.DNS) == 0 {
		return fmt.Errorf("at least one DNS server is required")
	}
	for name, v := range map[string]string{
		"locale": in.Locale, "timezone": in.Timezone, "keyboard layout": in.KeyboardLayout,
		"network device": in.Device, "ip address": in.IPAddress, "netmask": in.Netmask,
		"gateway": in.Gateway, "user groups": strings.ReplaceAll(in.UserGroups, ",", ""),
	} {
		if !tokenRe.MatchString(v) {
			return fmt.Errorf("invalid %s %q", name, v)
		}
	}
	for _, d := range in.DNS {
		if !tokenRe.MatchString(d) {
			return fmt.Errorf("invalid DNS server %q", d)
		}
	}
	for _, p := range in.Packages {
		if !tokenRe.MatchString(p) {
			return fmt.Errorf("invalid package name %q", p)
		}
	}
	for name, u := range map[string]string{"baseos url": in.BaseOSURL, "appstream url": in.AppStreamURL} {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") || strings.ContainsAny(u, "\"\n\r\\ ") {
			return fmt.Errorf("invalid %s %q", name, u)
		}
	}
	if in.PasswordHash != "" && strings.ContainsAny(in.PasswordHash, " \"\n\r\\'") {
		return fmt.Errorf("invalid password hash")
	}
	for _, k := range in.SSHPublicKeys {
		if k == "" || strings.ContainsAny(k, "\"\n\r\\") {
			return fmt.Errorf("invalid SSH public key")
		}
	}
	if err := cloudinit.ValidateMountOptions("root_mount_options", in.RootMountOptions); err != nil {
		return err
	}
	return cloudinit.ValidateMountOptions("data_disk_mount_options", in.DataDiskMountOptions)
}
