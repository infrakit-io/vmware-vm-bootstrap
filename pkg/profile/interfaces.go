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
	DataDiskMountPath string
	SwapSizeGB        *int
	OSVersion         string
	OSSchematicID     string

	VCenterHost     string
	VCenterUsername string
	VCenterPassword string
	VCenterInsecure bool
}

// Runtime provides live VMware/ISO resources for provisioning.
type Runtime struct {
	CreatedVM        *object.VirtualMachine
	Creator          vm.CreatorInterface
	ISOManager       iso.ManagerInterface
	ISODatastore     *object.Datastore
	ISODatastoreName string
	Logger           *slog.Logger
}

// Result carries profile-specific artifacts needed by bootstrap core.
type Result struct {
	NoCloudUploadPath string
}

// Provisioner is the OS-profile contract used by bootstrap flow.
type Provisioner interface {
	Name() string
	ProvisionAndBoot(ctx context.Context, in Input, rt Runtime) (Result, error)
	PostInstall(ctx context.Context, in Input, rt Runtime, res Result) error
}
