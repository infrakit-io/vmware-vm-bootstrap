package ubuntu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/internal/utils"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/cloudinit"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
	"github.com/google/uuid"
)

type Provisioner struct{}

func New() *Provisioner { return &Provisioner{} }

func (p *Provisioner) Name() string { return "ubuntu" }

func (p *Provisioner) ProvisionAndBoot(ctx context.Context, in profile.Input, rt profile.Runtime) (profile.Result, error) {
	generator, err := cloudinit.NewGenerator()
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to create cloud-init generator: %w", err)
	}

	var passwordHash string
	switch {
	case in.PasswordHash != "":
		passwordHash = in.PasswordHash
	case in.Password != "":
		hashed, hashErr := utils.HashPasswordBcrypt(in.Password)
		if hashErr != nil {
			return profile.Result{}, fmt.Errorf("failed to hash password: %w", hashErr)
		}
		passwordHash = hashed
	default:
		passwordHash = "*"
	}

	cidr, err := utils.NetmaskToCIDR(in.Netmask)
	if err != nil {
		return profile.Result{}, fmt.Errorf("invalid netmask: %w", err)
	}

	swapSizeGB := configs.Defaults.CloudInit.SwapSizeGB
	if in.SwapSizeGB != nil {
		swapSizeGB = *in.SwapSizeGB
	}
	swapSize := fmt.Sprintf("%dG", swapSizeGB)

	userData, err := generator.GenerateUserData(&cloudinit.UserDataInput{
		Hostname:          in.VMName,
		Username:          in.Username,
		PasswordHash:      passwordHash,
		SSHPublicKeys:     in.SSHPublicKeys,
		AllowPasswordSSH:  in.AllowPasswordSSH,
		Locale:            in.Locale,
		Timezone:          in.Timezone,
		KeyboardLayout:    configs.Defaults.CloudInit.KeyboardLayout,
		SwapSize:          swapSize,
		SwapSizeGB:        swapSizeGB,
		Packages:          configs.Defaults.CloudInit.Packages,
		UserGroups:        configs.Defaults.CloudInit.UserGroups,
		UserShell:         configs.Defaults.CloudInit.UserShell,
		InterfaceName:     in.NetworkInterface,
		DataDiskMountPath: in.DataDiskMountPath,
		IPAddress:         in.IPAddress,
		CIDR:              cidr,
		Gateway:           in.Gateway,
		DNS:               in.DNS,
	})
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to generate user-data: %w", err)
	}

	metaData, err := generator.GenerateMetaData(&cloudinit.MetaDataInput{
		InstanceID: uuid.New().String(),
		Hostname:   in.VMName,
	})
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to generate meta-data: %w", err)
	}

	networkConfig, err := generator.GenerateNetworkConfig(&cloudinit.NetworkConfigInput{
		InterfaceName: in.NetworkInterface,
		IPAddress:     in.IPAddress,
		CIDR:          cidr,
		Gateway:       in.Gateway,
		DNS:           in.DNS,
	})
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to generate network-config: %w", err)
	}

	rt.Logger.Info("Cloud-init configs generated")

	ubuntuISOPath, err := rt.ISOManager.DownloadUbuntu(in.OSVersion)
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to download Ubuntu ISO: %w", err)
	}
	rt.Logger.Info("Ubuntu ISO ready", "path", ubuntuISOPath)

	ubuntuISOPath, _, err = rt.ISOManager.ModifyUbuntuISO(ubuntuISOPath)
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to modify Ubuntu ISO: %w", err)
	}
	rt.Logger.Info("Ubuntu ISO modified for autoinstall", "path", ubuntuISOPath)

	nocloudISOPath, err := rt.ISOManager.CreateNoCloudISO(userData, metaData, networkConfig, in.VMName)
	if err != nil {
		return profile.Result{}, fmt.Errorf("failed to create NoCloud ISO: %w", err)
	}
	rt.Logger.Info("NoCloud ISO created", "path", nocloudISOPath)

	ubuntuUploadPath := fmt.Sprintf("ISO/ubuntu/%s", filepath.Base(ubuntuISOPath))
	nocloudUploadPath := fmt.Sprintf("ISO/nocloud/%s", filepath.Base(nocloudISOPath))

	if err := rt.ISOManager.UploadToDatastore(rt.ISODatastore, ubuntuISOPath, ubuntuUploadPath,
		in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
		return profile.Result{}, fmt.Errorf("failed to upload Ubuntu ISO: %w", err)
	}
	if err := rt.ISOManager.UploadAlways(rt.ISODatastore, nocloudISOPath, nocloudUploadPath,
		in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
		return profile.Result{}, fmt.Errorf("failed to upload NoCloud ISO: %w", err)
	}

	_ = os.Remove(nocloudISOPath)
	rt.Logger.Info("NoCloud ISO uploaded and cleaned up")
	rt.Logger.Info("ISOs uploaded to datastore")

	ubuntuMountPath := fmt.Sprintf("[%s] %s", rt.ISODatastoreName, ubuntuUploadPath)
	nocloudMountPath := fmt.Sprintf("[%s] %s", rt.ISODatastoreName, nocloudUploadPath)
	if err := rt.ISOManager.MountISOs(rt.CreatedVM, ubuntuMountPath, nocloudMountPath); err != nil {
		return profile.Result{}, fmt.Errorf("failed to mount ISOs: %w", err)
	}
	rt.Logger.Info("ISOs mounted to VM")

	if err := rt.Creator.PowerOn(rt.CreatedVM); err != nil {
		return profile.Result{}, fmt.Errorf("failed to power on VM: %w", err)
	}
	rt.Logger.Info("VM powered on - waiting for installation...")

	if err := rt.ISOManager.EnsureCDROMsConnectedAfterBoot(rt.CreatedVM); err != nil {
		rt.Logger.Warn("CD-ROM post-boot check failed (continuing)", "error", err)
	}

	return profile.Result{NoCloudUploadPath: nocloudUploadPath}, nil
}

func (p *Provisioner) PostInstall(ctx context.Context, in profile.Input, rt profile.Runtime, res profile.Result) error {
	rt.Logger.Info("Powering off VM to release CD-ROM file locks...")
	if err := rt.Creator.PowerOff(rt.CreatedVM); err != nil {
		rt.Logger.Warn("Failed to power off VM for cleanup (continuing)", "error", err)
		return nil
	}
	if err := rt.ISOManager.RemoveAllCDROMs(rt.CreatedVM); err != nil {
		rt.Logger.Warn("Failed to remove CD-ROMs (continuing)", "error", err)
	}
	if res.NoCloudUploadPath != "" {
		if err := rt.ISOManager.DeleteFromDatastore(rt.ISODatastoreName, res.NoCloudUploadPath,
			in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
			rt.Logger.Warn("Failed to delete NoCloud ISO from datastore (non-critical)", "error", err)
		} else {
			rt.Logger.Info("NoCloud ISO deleted from datastore", "path", res.NoCloudUploadPath)
		}
	}
	rt.Logger.Info("Powering VM back on...")
	if err := rt.Creator.PowerOn(rt.CreatedVM); err != nil {
		return fmt.Errorf("failed to power VM back on after cleanup: %w", err)
	}
	return nil
}
