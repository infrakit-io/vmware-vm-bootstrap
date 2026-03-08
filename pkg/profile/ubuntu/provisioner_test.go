package ubuntu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	isomocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
	vmmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm/mocks"
	"github.com/stretchr/testify/mock"
)

func TestProvisionAndBoot_InvalidNetmask(t *testing.T) {
	p := New()
	_, err := p.ProvisionAndBoot(context.Background(), profile.Input{
		VMName:           "vm1",
		Username:         "user",
		PasswordHash:     "$6$hash",
		NetworkInterface: "eth0",
		IPAddress:        "192.168.1.10",
		Netmask:          "bad-mask",
		Gateway:          "192.168.1.1",
		DNS:              []string{"1.1.1.1"},
	}, profile.Runtime{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || err.Error() == "" {
		t.Fatal("expected invalid netmask error")
	}
}

func TestProvisionAndBoot_Success(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}

	ubuntuISO := "/tmp/ubuntu.iso"
	modISO := "/tmp/ubuntu-mod.iso"
	nocloudISO := "/tmp/nocloud.iso"
	nocloudUploadPath := "ISO/nocloud/" + filepath.Base(nocloudISO)
	ubuntuUploadPath := "ISO/ubuntu/" + filepath.Base(modISO)

	isoMgr.On("DownloadUbuntu", "24.04").Return(ubuntuISO, nil).Once()
	isoMgr.On("ModifyUbuntuISO", ubuntuISO).Return(modISO, true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return(nocloudISO, nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, modISO, ubuntuUploadPath, "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("UploadAlways", mock.Anything, nocloudISO, nocloudUploadPath, "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountISOs", mock.Anything, "[ds] "+ubuntuUploadPath, "[ds] "+nocloudUploadPath).Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(nil).Once()
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(nil).Once()

	p := New()
	res, err := p.ProvisionAndBoot(context.Background(), profile.Input{
		VMName:            "vm1",
		Username:          "user",
		PasswordHash:      "$6$hash",
		NetworkInterface:  "eth0",
		IPAddress:         "192.168.1.10",
		Netmask:           "255.255.255.0",
		Gateway:           "192.168.1.1",
		DNS:               []string{"1.1.1.1"},
		OSVersion:         "24.04",
		VCenterHost:       "vc",
		VCenterUsername:   "user",
		VCenterPassword:   "pass",
		VCenterInsecure:   true,
		DataDiskMountPath: "/data",
	}, profile.Runtime{
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastoreName: "ds",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.NoCloudUploadPath != nocloudUploadPath {
		t.Fatalf("unexpected nocloud upload path: %s", res.NoCloudUploadPath)
	}

	isoMgr.AssertExpectations(t)
	creator.AssertExpectations(t)
}

func TestPostInstall_Success(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	creator.On("PowerOff", mock.Anything).Return(nil).Once()
	isoMgr.On("RemoveAllCDROMs", mock.Anything).Return(nil).Once()
	isoMgr.On("DeleteFromDatastore", "ds", "ISO/nocloud/vm1.iso", "vc", "user", "pass", true).Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(nil).Once()

	p := New()
	err := p.PostInstall(context.Background(), profile.Input{
		VCenterHost:     "vc",
		VCenterUsername: "user",
		VCenterPassword: "pass",
		VCenterInsecure: true,
	}, profile.Runtime{
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastoreName: "ds",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, profile.Result{
		NoCloudUploadPath: "ISO/nocloud/vm1.iso",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	isoMgr.AssertExpectations(t)
	creator.AssertExpectations(t)
}

func TestPostInstall_PowerOffErrorIsNonFatal(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	creator.On("PowerOff", mock.Anything).Return(assertErr("poweroff failed")).Once()

	p := New()
	err := p.PostInstall(context.Background(), profile.Input{}, profile.Runtime{
		Creator:    creator,
		ISOManager: isoMgr,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, profile.Result{})
	if err != nil {
		t.Fatalf("expected nil err, got: %v", err)
	}
	creator.AssertExpectations(t)
}

func TestPostInstall_PowerOnError(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	creator.On("PowerOff", mock.Anything).Return(nil).Once()
	isoMgr.On("RemoveAllCDROMs", mock.Anything).Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(assertErr("poweron failed")).Once()

	p := New()
	err := p.PostInstall(context.Background(), profile.Input{}, profile.Runtime{
		Creator:    creator,
		ISOManager: isoMgr,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, profile.Result{})
	if err == nil || err.Error() == "" {
		t.Fatal("expected power-on error")
	}
	isoMgr.AssertExpectations(t)
	creator.AssertExpectations(t)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func baseUbuntuInput() profile.Input {
	return profile.Input{
		VMName:            "vm1",
		Username:          "user",
		PasswordHash:      "$6$hash",
		NetworkInterface:  "eth0",
		IPAddress:         "192.168.1.10",
		Netmask:           "255.255.255.0",
		Gateway:           "192.168.1.1",
		DNS:               []string{"1.1.1.1"},
		OSVersion:         "24.04",
		VCenterHost:       "vc",
		VCenterUsername:   "user",
		VCenterPassword:   "pass",
		VCenterInsecure:   true,
		DataDiskMountPath: "/data",
	}
}

func baseUbuntuRuntime(isoMgr *isomocks.ManagerInterface, creator *vmmocks.CreatorInterface) profile.Runtime {
	return profile.Runtime{
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastoreName: "ds",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestProvisionerName(t *testing.T) {
	if New().Name() != "ubuntu" {
		t.Fatalf("unexpected profile name: %q", New().Name())
	}
}

func TestProvisionAndBoot_DownloadUbuntuFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("", errors.New("download failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected download error")
	}
}

func TestProvisionAndBoot_ModifyUbuntuFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("", false, errors.New("modify failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected modify error")
	}
}

func TestProvisionAndBoot_CreateNoCloudFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("", errors.New("nocloud failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected nocloud error")
	}
}

func TestProvisionAndBoot_UploadUbuntuFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("/tmp/nocloud.iso", nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, "/tmp/ubuntu-mod.iso", "ISO/ubuntu/ubuntu-mod.iso", "vc", "user", "pass", true).Return(errors.New("upload failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected upload ubuntu error")
	}
}

func TestProvisionAndBoot_UploadNoCloudFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("/tmp/nocloud.iso", nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, "/tmp/ubuntu-mod.iso", "ISO/ubuntu/ubuntu-mod.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("UploadAlways", mock.Anything, "/tmp/nocloud.iso", "ISO/nocloud/nocloud.iso", "vc", "user", "pass", true).Return(errors.New("upload nocloud failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected upload nocloud error")
	}
}

func TestProvisionAndBoot_MountFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("/tmp/nocloud.iso", nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, "/tmp/ubuntu-mod.iso", "ISO/ubuntu/ubuntu-mod.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("UploadAlways", mock.Anything, "/tmp/nocloud.iso", "ISO/nocloud/nocloud.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountISOs", mock.Anything, "[ds] ISO/ubuntu/ubuntu-mod.iso", "[ds] ISO/nocloud/nocloud.iso").Return(errors.New("mount failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected mount error")
	}
}

func TestProvisionAndBoot_PowerOnFails(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("/tmp/nocloud.iso", nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, "/tmp/ubuntu-mod.iso", "ISO/ubuntu/ubuntu-mod.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("UploadAlways", mock.Anything, "/tmp/nocloud.iso", "ISO/nocloud/nocloud.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountISOs", mock.Anything, "[ds] ISO/ubuntu/ubuntu-mod.iso", "[ds] ISO/nocloud/nocloud.iso").Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(errors.New("power on failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err == nil {
		t.Fatal("expected power-on error")
	}
}

func TestProvisionAndBoot_CDRomWarningNonFatal(t *testing.T) {
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil).Once()
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-mod.iso", true, nil).Once()
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "vm1").Return("/tmp/nocloud.iso", nil).Once()
	isoMgr.On("UploadToDatastore", mock.Anything, "/tmp/ubuntu-mod.iso", "ISO/ubuntu/ubuntu-mod.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("UploadAlways", mock.Anything, "/tmp/nocloud.iso", "ISO/nocloud/nocloud.iso", "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountISOs", mock.Anything, "[ds] ISO/ubuntu/ubuntu-mod.iso", "[ds] ISO/nocloud/nocloud.iso").Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(nil).Once()
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(errors.New("warn")).Once()

	res, err := New().ProvisionAndBoot(context.Background(), baseUbuntuInput(), baseUbuntuRuntime(isoMgr, creator))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if res.NoCloudUploadPath != "ISO/nocloud/nocloud.iso" {
		t.Fatalf("unexpected nocloud upload path: %s", res.NoCloudUploadPath)
	}
}
