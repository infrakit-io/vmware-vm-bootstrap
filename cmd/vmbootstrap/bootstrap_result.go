package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
	pkgconfig "github.com/infrakit-io/vmware-vm-bootstrap/pkg/config"
)

func writeBootstrapResult(path string, cfg *bootstrap.VMConfig, sshKeyPath string, sshPort int, vm *bootstrap.VM) error {
	keyPath := resolveSSHPrivateKeyPath(sshKeyPath)
	if keyPath == "" {
		return fmt.Errorf("cannot write bootstrap result: vm.ssh_key_path is required (private key path not available)")
	}
	fp, err := computeSSHHostFingerprint(vm.IPAddress, sshPort)
	if err != nil {
		return fmt.Errorf("compute ssh host fingerprint: %w", err)
	}

	result := pkgconfig.BootstrapResult{
		VMName:             cfg.Name,
		IPAddress:          vm.IPAddress,
		SSHUser:            cfg.Username,
		SSHPrivateKey:      keyPath,
		SSHPort:            sshPort,
		SSHHostFingerprint: fp,
	}
	if result.SSHPort == 0 {
		result.SSHPort = 22
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create bootstrap result dir: %w", err)
	}
	if err := pkgconfig.SaveBootstrapResult(path, result); err != nil {
		return fmt.Errorf("save bootstrap result: %w", err)
	}
	return nil
}

func resolveSSHPrivateKeyPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(p), ".pub") {
		priv := strings.TrimSuffix(p, ".pub")
		if st, err := os.Stat(priv); err == nil && !st.IsDir() {
			return priv
		}
	}
	return p
}

func resolveBootstrapResultPath(explicitPath, vmName string) string {
	path := strings.TrimSpace(explicitPath)
	if path == "" {
		if !configs.Defaults.Output.Enable {
			return ""
		}
		path = strings.TrimSpace(configs.Defaults.Output.BootstrapResultPath)
	}
	if path == "" {
		return ""
	}
	if strings.Contains(path, "{vm}") {
		path = strings.ReplaceAll(path, "{vm}", vmName)
	}
	return path
}
