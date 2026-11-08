package iso

import (
	"github.com/vmware/govmomi/object"
)

// compile-time interface compliance check
var _ ManagerInterface = (*Manager)(nil)

// ManagerInterface abstracts ISO download, creation, upload and mount operations.
// The real implementation uses govmomi + system tools; tests inject a mock.
type ManagerInterface interface {
	DownloadUbuntu(version string) (string, error)
	ModifyUbuntuISO(originalISOPath string) (string, bool, error)
	DownloadAlma(version string) (string, error)
	ModifyAlmaISO(originalISOPath string) (string, bool, error)
	CreateKickstartISO(kickstart, vmName string) (string, error)
	CreateNoCloudISO(userData, metaData, networkConfig, vmName string) (string, error)
	UploadToDatastore(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error
	UploadAlways(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error
	MountSingleISO(vm *object.VirtualMachine, isoPath, label string) error
	MountISOs(vm *object.VirtualMachine, ubuntuISO, nocloudISO string) error
	RemoveAllCDROMs(vm *object.VirtualMachine) error
	EnsureCDROMsConnectedAfterBoot(vm *object.VirtualMachine) error
	DeleteFromDatastore(datastoreName, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error
}
