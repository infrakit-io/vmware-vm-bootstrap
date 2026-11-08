// Package mocks provides testify-based mock implementations for testing
// without a real vCenter connection or system ISO tools.
package mocks

import (
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/object"
)

// ManagerInterface is a mock for iso.ManagerInterface.
type ManagerInterface struct {
	mock.Mock
}

func (m *ManagerInterface) DownloadUbuntu(version string) (string, error) {
	args := m.Called(version)
	return args.String(0), args.Error(1)
}

func (m *ManagerInterface) ModifyUbuntuISO(originalISOPath string) (string, bool, error) {
	args := m.Called(originalISOPath)
	return args.String(0), args.Bool(1), args.Error(2)
}

func (m *ManagerInterface) DownloadAlma(version string) (string, error) {
	args := m.Called(version)
	return args.String(0), args.Error(1)
}

func (m *ManagerInterface) ModifyAlmaISO(originalISOPath string) (string, bool, error) {
	args := m.Called(originalISOPath)
	return args.String(0), args.Bool(1), args.Error(2)
}

func (m *ManagerInterface) CreateKickstartISO(kickstart, vmName string) (string, error) {
	args := m.Called(kickstart, vmName)
	return args.String(0), args.Error(1)
}

func (m *ManagerInterface) CreateNoCloudISO(userData, metaData, networkConfig, vmName string) (string, error) {
	args := m.Called(userData, metaData, networkConfig, vmName)
	return args.String(0), args.Error(1)
}

func (m *ManagerInterface) UploadToDatastore(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	args := m.Called(ds, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure)
	return args.Error(0)
}

func (m *ManagerInterface) UploadAlways(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	args := m.Called(ds, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure)
	return args.Error(0)
}

func (m *ManagerInterface) MountSingleISO(vm *object.VirtualMachine, isoPath, label string) error {
	args := m.Called(vm, isoPath, label)
	return args.Error(0)
}

func (m *ManagerInterface) MountISOs(vm *object.VirtualMachine, ubuntuISO, nocloudISO string) error {
	args := m.Called(vm, ubuntuISO, nocloudISO)
	return args.Error(0)
}

func (m *ManagerInterface) RemoveAllCDROMs(vm *object.VirtualMachine) error {
	args := m.Called(vm)
	return args.Error(0)
}

func (m *ManagerInterface) EnsureCDROMsConnectedAfterBoot(vm *object.VirtualMachine) error {
	args := m.Called(vm)
	return args.Error(0)
}

func (m *ManagerInterface) DeleteFromDatastore(datastoreName, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	args := m.Called(datastoreName, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure)
	return args.Error(0)
}
