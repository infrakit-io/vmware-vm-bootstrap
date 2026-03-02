package ubuntu

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	isomocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile"
	vmmocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vm/mocks"
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
