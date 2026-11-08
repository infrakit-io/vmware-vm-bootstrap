// Package alma provisions AlmaLinux (EL9) VMs with an unattended Anaconda
// kickstart install delivered on an OEMDRV-labelled CD-ROM.
package alma

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/internal/utils"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/kickstart"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
)

type Provisioner struct{}

func New() *Provisioner { return &Provisioner{} }

func (p *Provisioner) Name() string { return "alma" }

// Prefetch downloads + checksum-verifies the AlmaLinux boot ISO and patches its
// boot menu. None of it depends on the VM, so it runs before VM creation.
func (p *Provisioner) Prefetch(ctx context.Context, in profile.Input, rt profile.Runtime) (string, error) {
	return p.fetchISO(in, rt)
}

func (p *Provisioner) fetchISO(in profile.Input, rt profile.Runtime) (string, error) {
	isoPath, err := rt.ISOManager.DownloadAlma(in.OSVersion)
	if err != nil {
		return "", fmt.Errorf("failed to download AlmaLinux ISO: %w", err)
	}
	rt.Logger.Info("AlmaLinux ISO ready", "path", isoPath)
	isoPath, _, err = rt.ISOManager.ModifyAlmaISO(isoPath)
	if err != nil {
		return "", fmt.Errorf("failed to patch AlmaLinux ISO: %w", err)
	}
	rt.Logger.Info("AlmaLinux ISO patched for unattended install", "path", isoPath)
	return isoPath, nil
}

// BuildKickstart renders ks.cfg for the given input (used by ProvisionAndBoot and tests).
func BuildKickstart(in profile.Input) (string, error) {
	release, ok := configs.AlmaReleases.Releases[in.OSVersion]
	if !ok {
		return "", fmt.Errorf("unsupported AlmaLinux version %q", in.OSVersion)
	}
	if len(in.ExtraNICs) > 0 {
		return "", fmt.Errorf("ExtraNICs are not supported by the alma profile")
	}

	var passwordHash string
	switch {
	case in.PasswordHash != "":
		passwordHash = in.PasswordHash
	case in.Password != "":
		hashed, err := utils.HashPasswordBcrypt(in.Password)
		if err != nil {
			return "", fmt.Errorf("failed to hash password: %w", err)
		}
		passwordHash = hashed
	}

	device := strings.ToLower(strings.TrimSpace(in.MACAddress))
	if device == "" {
		device = "link"
	}
	swapGB := configs.Defaults.CloudInit.SwapSizeGB
	if in.SwapSizeGB != nil {
		swapGB = *in.SwapSizeGB
	}
	d := configs.Defaults.Alma
	return kickstart.GenerateAlma(&kickstart.AlmaInput{
		Hostname:             in.VMName,
		Username:             in.Username,
		PasswordHash:         passwordHash,
		SSHPublicKeys:        in.SSHPublicKeys,
		AllowPasswordSSH:     in.AllowPasswordSSH,
		Locale:               in.Locale,
		Timezone:             in.Timezone,
		KeyboardLayout:       configs.Defaults.CloudInit.KeyboardLayout,
		Device:               device,
		IPAddress:            in.IPAddress,
		Netmask:              in.Netmask,
		Gateway:              in.Gateway,
		DNS:                  in.DNS,
		Filesystem:           d.Filesystem,
		BootSizeMB:           d.BootSizeMB,
		DataDiskMountPath:    in.DataDiskMountPath,
		RootMountOptions:     in.RootMountOptions,
		DataDiskMountOptions: in.DataDiskMountOptions,
		SwapSizeGB:           swapGB,
		Packages:             d.Packages,
		UserGroups:           d.UserGroups,
		BaseOSURL:            release.BaseOSURL,
		AppStreamURL:         release.AppStreamURL,
	})
}

func (p *Provisioner) ProvisionAndBoot(ctx context.Context, in profile.Input, rt profile.Runtime) (profile.Result, error) {
	ks, err := BuildKickstart(in)
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to generate kickstart: %w", err)
	}
	rt.Logger.Info("Kickstart generated")

	almaISOPath := rt.PrefetchedISOPath
	if almaISOPath == "" {
		if almaISOPath, err = p.fetchISO(in, rt); err != nil {
			return profile.Result{}, err
		}
	}

	ksISOPath, err := rt.ISOManager.CreateKickstartISO(ks, in.VMName)
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to create kickstart ISO: %w", err)
	}
	defer func() { _ = os.Remove(ksISOPath) }()

	almaUploadPath := fmt.Sprintf("ISO/alma/%s", filepath.Base(almaISOPath))
	ksUploadPath := fmt.Sprintf("ISO/oemdrv/%s", filepath.Base(ksISOPath))
	if err := rt.ISOManager.UploadToDatastore(rt.ISODatastore, almaISOPath, almaUploadPath,
		in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
		return profile.Result{}, fmt.Errorf("failed to upload AlmaLinux ISO: %w", err)
	}
	if err := rt.ISOManager.UploadAlways(rt.ISODatastore, ksISOPath, ksUploadPath,
		in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
		return profile.Result{}, fmt.Errorf("failed to upload kickstart ISO: %w", err)
	}
	res := profile.Result{NoCloudUploadPath: ksUploadPath}

	almaMount := fmt.Sprintf("[%s] %s", rt.ISODatastoreName, almaUploadPath)
	ksMount := fmt.Sprintf("[%s] %s", rt.ISODatastoreName, ksUploadPath)
	if err := rt.ISOManager.MountISOs(rt.CreatedVM, almaMount, ksMount); err != nil {
		return res, fmt.Errorf("failed to mount ISOs: %w", err)
	}
	if err := rt.Creator.PowerOn(rt.CreatedVM); err != nil {
		return res, fmt.Errorf("failed to power on VM: %w", err)
	}
	rt.Logger.Info("VM powered on - waiting for kickstart install to power it off...")
	if err := rt.ISOManager.EnsureCDROMsConnectedAfterBoot(rt.CreatedVM); err != nil {
		rt.Logger.Warn("CD-ROM post-boot check failed (continuing)", "error", err)
	}
	return res, nil
}

// PostInstall runs after the kickstart's `poweroff`: detach the CD-ROMs (so the
// installer cannot boot again), drop the kickstart ISO and power the VM back on.
func (p *Provisioner) PostInstall(ctx context.Context, in profile.Input, rt profile.Runtime, res profile.Result) error {
	if err := rt.Creator.PowerOff(rt.CreatedVM); err != nil { // no-op when already off
		return fmt.Errorf("failed to ensure VM is powered off: %w", err)
	}
	if err := rt.ISOManager.RemoveAllCDROMs(rt.CreatedVM); err != nil {
		return fmt.Errorf("failed to remove CD-ROMs (refusing to boot with installer media attached): %w", err)
	}
	if res.NoCloudUploadPath != "" {
		if err := rt.ISOManager.DeleteFromDatastore(rt.ISODatastoreName, res.NoCloudUploadPath,
			in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
			rt.Logger.Warn("Failed to delete kickstart ISO from datastore (non-critical)", "error", err)
		}
	}
	rt.Logger.Info("Powering VM on from the installed disk...")
	if err := rt.Creator.PowerOn(rt.CreatedVM); err != nil {
		return fmt.Errorf("failed to power VM on after install: %w", err)
	}
	return nil
}
