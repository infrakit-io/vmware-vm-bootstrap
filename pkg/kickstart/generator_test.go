package kickstart

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func baseInput() AlmaInput {
	return AlmaInput{
		Hostname:       "da1",
		Username:       "root",
		SSHPublicKeys:  []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample admin@mgmt"},
		Locale:         "en_US.UTF-8",
		Timezone:       "Europe/Bucharest",
		KeyboardLayout: "us",
		Device:         "00:50:56:aa:bb:cc",
		IPAddress:      "10.0.0.10",
		Netmask:        "255.255.255.0",
		Gateway:        "10.0.0.1",
		DNS:            []string{"10.0.0.2", "1.1.1.1"},
		Filesystem:     "ext4",
		BootSizeMB:     1024,
		Packages:       []string{"open-vm-tools", "sudo"},
		UserGroups:     "wheel",
		BaseOSURL:      "https://repo.example/9.8/BaseOS/x86_64/os/",
		AppStreamURL:   "https://repo.example/9.8/AppStream/x86_64/os/",
	}
}

func TestGenerateAlma_Golden(t *testing.T) {
	cases := map[string]func(in *AlmaInput){
		"root-key-only": func(in *AlmaInput) {},
		"root-data-swap-mountopts": func(in *AlmaInput) {
			in.DataDiskMountPath = "/home"
			in.SwapSizeGB = 4
			in.DataDiskMountOptions = []string{"usrquota", "grpquota", "nodev"}
			in.RootMountOptions = []string{"noatime"}
		},
		"user-password-ssh-on": func(in *AlmaInput) {
			in.Username = "deploy"
			in.PasswordHash = "$2b$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
			in.AllowPasswordSSH = true
		},
		"user-password-ssh-off": func(in *AlmaInput) {
			in.Username = "deploy"
			in.PasswordHash = "$2b$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
		},
		"root-password-ssh-on": func(in *AlmaInput) {
			in.PasswordHash = "$2b$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
			in.AllowPasswordSSH = true
			in.Device = "link"
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			in := baseInput()
			mod(&in)
			got, err := GenerateAlma(&in)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name+".ks.golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			if got != string(want) {
				t.Fatalf("kickstart differs from %s\n--- got ---\n%s", path, got)
			}
		})
	}
}

func TestGenerateAlma_Properties(t *testing.T) {
	in := baseInput()
	in.DataDiskMountPath = "/home"
	in.SwapSizeGB = 2
	ks, err := GenerateAlma(&in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"network --bootproto=static --device=00:50:56:aa:bb:cc --ip=10.0.0.10 --netmask=255.255.255.0 --gateway=10.0.0.1 --nameserver=10.0.0.2,1.1.1.1 --hostname=da1",
		"rootpw --lock", `sshkey --username=root "ssh-ed25519`, "part swap --size=2048 --ondisk=sda",
		"part /home --fstype=ext4 --grow --size=1024 --ondisk=sdb", "ignoredisk --only-use=sda,sdb",
		"poweroff", "reqpart", "PasswordAuthentication no", "open-vm-tools",
	} {
		if !strings.Contains(ks, want) {
			t.Errorf("kickstart missing %q", want)
		}
	}
	if strings.Contains(ks, "NOPASSWD") {
		t.Error("root install must not write a sudoers rule")
	}
	in.DataDiskMountPath, in.SwapSizeGB = "", 0
	ks, _ = GenerateAlma(&in)
	if strings.Contains(ks, "swap") || strings.Contains(ks, "sdb") {
		t.Error("no swap/data disk expected")
	}
}

func TestGenerateAlma_Validation(t *testing.T) {
	bad := map[string]func(in *AlmaInput){
		"username injection":  func(in *AlmaInput) { in.Username = "x\n%post" },
		"hostname":            func(in *AlmaInput) { in.Hostname = "a b" },
		"filesystem":          func(in *AlmaInput) { in.Filesystem = "btrfs" },
		"mount path":          func(in *AlmaInput) { in.DataDiskMountPath = "/home; rm -rf /" },
		"mount path relative": func(in *AlmaInput) { in.DataDiskMountPath = "home" },
		"mount root":          func(in *AlmaInput) { in.DataDiskMountPath = "/" },
		"mount option":        func(in *AlmaInput) { in.RootMountOptions = []string{"evil"} },
		"ssh key quote":       func(in *AlmaInput) { in.SSHPublicKeys = []string{`a" b`} },
		"hash space":          func(in *AlmaInput) { in.PasswordHash = "a b" },
		"no dns":              func(in *AlmaInput) { in.DNS = nil },
		"negative swap":       func(in *AlmaInput) { in.SwapSizeGB = -1 },
		"url scheme":          func(in *AlmaInput) { in.BaseOSURL = "ftp://x" },
		"device":              func(in *AlmaInput) { in.Device = "eth0 --foo" },
	}
	for name, mod := range bad {
		t.Run(name, func(t *testing.T) {
			in := baseInput()
			mod(&in)
			if _, err := GenerateAlma(&in); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
