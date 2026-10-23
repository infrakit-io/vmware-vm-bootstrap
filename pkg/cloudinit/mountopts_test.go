package cloudinit

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func renderMounts(t *testing.T, in *UserDataInput) map[string]map[string]any {
	t.Helper()
	gen, err := NewGenerator()
	if err != nil {
		t.Fatal(err)
	}
	out, err := gen.GenerateUserData(in)
	if err != nil {
		t.Fatalf("GenerateUserData: %v", err)
	}
	var doc struct {
		Autoinstall struct {
			Storage struct {
				Config []map[string]any `yaml:"config"`
			} `yaml:"storage"`
		} `yaml:"autoinstall"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, out)
	}
	mounts := map[string]map[string]any{}
	for _, a := range doc.Autoinstall.Storage.Config {
		if a["type"] == "mount" {
			mounts[a["id"].(string)] = a
		}
	}
	return mounts
}

func mountInput() *UserDataInput {
	return &UserDataInput{
		Hostname: "h", Username: "u", PasswordHash: "x",
		SSHPublicKeys: []string{"ssh-ed25519 AAAA t@e"},
		Locale:        "en_US.UTF-8", Timezone: "UTC", KeyboardLayout: "us",
		SwapSize: "2G", SwapSizeGB: 2, Packages: []string{"open-vm-tools"},
		UserGroups: "sudo", UserShell: "/bin/bash",
		IPAddress: "192.168.1.10", CIDR: 24, Gateway: "192.168.1.1", DNS: []string{"8.8.8.8"},
		DataDiskMountPath: "/home",
	}
}

func TestMountOptions_DefaultUnchanged(t *testing.T) {
	m := renderMounts(t, mountInput())
	for _, id := range []string{"root-mount", "data-mount"} {
		if _, ok := m[id]["options"]; ok {
			t.Errorf("%s must have no options by default", id)
		}
	}
}

func TestMountOptions_RootQuota(t *testing.T) {
	in := mountInput()
	in.RootMountOptions = []string{"usrquota", "grpquota"}
	m := renderMounts(t, in)
	if got := m["root-mount"]["options"]; got != "defaults,usrquota,grpquota" {
		t.Errorf("root options = %v", got)
	}
	if _, ok := m["data-mount"]["options"]; ok {
		t.Error("data-mount must be unchanged")
	}
}

func TestMountOptions_DataDisk(t *testing.T) {
	in := mountInput()
	in.DataDiskMountOptions = []string{"nosuid", "usrquota", "grpquota", "usrjquota=aquota.user", "grpjquota=aquota.group", "jqfmt=vfsv1"}
	m := renderMounts(t, in)
	want := "defaults,nosuid,usrquota,grpquota,usrjquota=aquota.user,grpjquota=aquota.group,jqfmt=vfsv1"
	if got := m["data-mount"]["options"]; got != want {
		t.Errorf("data options = %v", got)
	}
	if _, ok := m["root-mount"]["options"]; ok {
		t.Error("root-mount must be unchanged")
	}
}

func TestMountOptions_AllowListRejects(t *testing.T) {
	for _, bad := range []string{"x\"\n  evil: 1", "usrquota,rw", "suid ", "foo", ""} {
		in := mountInput()
		in.RootMountOptions = []string{bad}
		gen, _ := NewGenerator()
		if _, err := gen.GenerateUserData(in); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("option %q: expected rejection, got %v", bad, err)
		}
	}
	if err := ValidateMountOptions("f", []string{"nodev", "defaults"}); err != nil {
		t.Errorf("valid options rejected: %v", err)
	}
}
