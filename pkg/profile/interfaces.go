package profile

import (
	"context"
	"log/slog"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	"github.com/vmware/govmomi/object"
)

// Input is normalized VM provisioning data consumed by OS profiles.
type Input struct {
	VMName            string
	Username          string
	Password          string
	PasswordHash      string
	SSHPublicKeys     []string
	AllowPasswordSSH  bool
	Timezone          string
	Locale            string
	NetworkInterface  string
	IPAddress         string
	Netmask           string
	Gateway           string
	DNS               []string
	ExtraNICs         []ExtraNIC
	DataDiskMountPath string
	SwapSizeGB        *int
	OSVersion         string
	OSSchematicID     string

	VCenterHost     string
	VCenterUsername string
	VCenterPassword string
	VCenterInsecure bool
}

// ExtraNIC is an additional static-IP NIC (no gateway) already attached to the VM.
type ExtraNIC struct {
	MACAddress string
	IPAddress  string
	Netmask    string
}

// Runtime provides live VMware/ISO resources for provisioning.
type Runtime struct {
	CreatedVM        *object.VirtualMachine
	Creator          vm.CreatorInterface
	ISOManager       iso.ManagerInterface
	ISODatastore     *object.Datastore
	ISODatastoreName string
	Logger           *slog.Logger

	// PrefetchedISOPath is the verified, ready-to-upload boot ISO produced by
	// Prefetch (empty when the profile has no Prefetch step).
	PrefetchedISOPath string
}

// Result carries profile-specific artifacts needed by bootstrap core.
type Result struct {
	NoCloudUploadPath string
}

// Prefetcher is an optional Provisioner capability: slow, VM-independent work
// (ISO download + checksum verification) done BEFORE the VM is created, so a
// failure leaves no VM behind. Runtime.CreatedVM is nil during Prefetch.
type Prefetcher interface {
	Prefetch(ctx context.Context, in Input, rt Runtime) (isoPath string, err error)
}

// Provisioner is the OS-profile contract used by bootstrap flow.
type Provisioner interface {
	Name() string
	ProvisionAndBoot(ctx context.Context, in Input, rt Runtime) (Result, error)
	PostInstall(ctx context.Context, in Input, rt Runtime, res Result) error
}
