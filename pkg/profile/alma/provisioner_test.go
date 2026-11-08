package alma

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	isomocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
	vmmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm/mocks"
	"github.com/stretchr/testify/mock"
)

func input() profile.Input {
	return profile.Input{
		VMName: "da1", Username: "root", SSHPublicKeys: []string{"ssh-ed25519 AAAA k"},
		Timezone: "UTC", Locale: "en_US.UTF-8", MACAddress: "00:50:56:AA:BB:CC",
		IPAddress: "10.0.0.10", Netmask: "255.255.255.0", Gateway: "10.0.0.1",
		DNS: []string{"10.0.0.2"}, OSVersion: "9.8", DataDiskMountPath: "/home",
		VCenterHost: "vc", VCenterUsername: "u", VCenterPassword: "p", VCenterInsecure: true,
	}
}

func rt(iso *isomocks.ManagerInterface, c *vmmocks.CreatorInterface) profile.Runtime {
	return profile.Runtime{Creator: c, ISOManager: iso, ISODatastoreName: "ds",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestName(t *testing.T) {
	if New().Name() != "alma" {
		t.Fatal("wrong name")
	}
}

func TestBuildKickstart(t *testing.T) {
	ks, err := BuildKickstart(input())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--device=00:50:56:aa:bb:cc", "part /home ", "https://repo.almalinux.org/almalinux/9.8/BaseOS/"} {
		if !strings.Contains(ks, want) {
			t.Errorf("missing %q", want)
		}
	}
	in := input()
	in.OSVersion = "9.99"
	if _, err := BuildKickstart(in); err == nil {
		t.Error("unknown version must fail")
	}
	in = input()
	in.ExtraNICs = []profile.ExtraNIC{{MACAddress: "00:50:56:00:00:01", IPAddress: "10.1.0.1", Netmask: "255.255.255.0"}}
	if _, err := BuildKickstart(in); err == nil {
		t.Error("extra NICs must be rejected")
	}
	in = input()
	in.MACAddress = ""
	if ks, _ := BuildKickstart(in); !strings.Contains(ks, "--device=link") {
		t.Error("expected --device=link fallback")
	}
}

func TestPrefetch(t *testing.T) {
	iso := &isomocks.ManagerInterface{}
	iso.On("DownloadAlma", "9.8").Return("/c/a.iso", nil).Once()
	iso.On("ModifyAlmaISO", "/c/a.iso").Return("/c/a-unattended.iso", true, nil).Once()
	got, err := New().Prefetch(context.Background(), input(), rt(iso, nil))
	if err != nil || got != "/c/a-unattended.iso" {
		t.Fatalf("got %q %v", got, err)
	}
	iso.AssertExpectations(t)

	iso = &isomocks.ManagerInterface{}
	iso.On("DownloadAlma", "9.8").Return("", errors.New("checksum mismatch")).Once()
	if _, err := New().Prefetch(context.Background(), input(), rt(iso, nil)); err == nil {
		t.Fatal("download error must propagate")
	}
}

func TestProvisionAndBoot_Success(t *testing.T) {
	iso := &isomocks.ManagerInterface{}
	c := &vmmocks.CreatorInterface{}
	var ksSeen string
	iso.On("CreateKickstartISO", mock.Anything, "da1").Run(func(a mock.Arguments) { ksSeen = a.String(0) }).
		Return("/tmp/oemdrv-da1.iso", nil).Once()
	iso.On("UploadToDatastore", mock.Anything, "/c/a-unattended.iso", "ISO/alma/a-unattended.iso", "vc", "u", "p", true).Return(nil).Once()
	iso.On("UploadAlways", mock.Anything, "/tmp/oemdrv-da1.iso", "ISO/oemdrv/oemdrv-da1.iso", "vc", "u", "p", true).Return(nil).Once()
	iso.On("MountISOs", mock.Anything, "[ds] ISO/alma/a-unattended.iso", "[ds] ISO/oemdrv/oemdrv-da1.iso").Return(nil).Once()
	c.On("PowerOn", mock.Anything).Return(nil).Once()
	iso.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(nil).Once()

	r := rt(iso, c)
	r.PrefetchedISOPath = "/c/a-unattended.iso"
	res, err := New().ProvisionAndBoot(context.Background(), input(), r)
	if err != nil {
		t.Fatal(err)
	}
	if res.NoCloudUploadPath != "ISO/oemdrv/oemdrv-da1.iso" {
		t.Fatalf("unexpected upload path %q", res.NoCloudUploadPath)
	}
	if !strings.Contains(ksSeen, "poweroff") {
		t.Error("kickstart not passed to CreateKickstartISO")
	}
	iso.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestProvisionAndBoot_BadInputFailsBeforeAnyUpload(t *testing.T) {
	in := input()
	in.OSVersion = "1.0"
	if _, err := New().ProvisionAndBoot(context.Background(), in, rt(&isomocks.ManagerInterface{}, nil)); err == nil {
		t.Fatal("expected error")
	}
}

func TestPostInstall(t *testing.T) {
	iso := &isomocks.ManagerInterface{}
	c := &vmmocks.CreatorInterface{}
	c.On("PowerOff", mock.Anything).Return(nil).Once()
	iso.On("RemoveAllCDROMs", mock.Anything).Return(nil).Once()
	iso.On("DeleteFromDatastore", "ds", "ISO/oemdrv/x.iso", "vc", "u", "p", true).Return(nil).Once()
	c.On("PowerOn", mock.Anything).Return(nil).Once()
	if err := New().PostInstall(context.Background(), input(), rt(iso, c), profile.Result{NoCloudUploadPath: "ISO/oemdrv/x.iso"}); err != nil {
		t.Fatal(err)
	}
	iso.AssertExpectations(t)
	c.AssertExpectations(t)
}

// Booting with the installer CD still attached would re-run the install: never power on in that case.
func TestPostInstall_CDROMRemovalFailureIsFatalAndNoPowerOn(t *testing.T) {
	iso := &isomocks.ManagerInterface{}
	c := &vmmocks.CreatorInterface{}
	c.On("PowerOff", mock.Anything).Return(nil).Once()
	iso.On("RemoveAllCDROMs", mock.Anything).Return(errors.New("boom")).Once()
	if err := New().PostInstall(context.Background(), input(), rt(iso, c), profile.Result{}); err == nil {
		t.Fatal("expected error")
	}
	c.AssertNotCalled(t, "PowerOn", mock.Anything)
}
