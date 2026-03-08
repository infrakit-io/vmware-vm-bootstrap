package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/vmware/govmomi/vim25/types"
)

// CreateNode provisions a new node using the selected OS profile.
func CreateNode(ctx context.Context, cfg *VMConfig) (*VM, error) {
	return BootstrapWithLogger(ctx, cfg, defaultLogger)
}

// CreateNodeWithLogger provisions a new node with a custom logger.
func CreateNodeWithLogger(ctx context.Context, cfg *VMConfig, logger *slog.Logger) (*VM, error) {
	return BootstrapWithLogger(ctx, cfg, logger)
}

// NodeExists reports whether a node with cfg.Name exists in cfg.Datacenter.
func NodeExists(ctx context.Context, cfg *VMConfig) (bool, error) {
	cfg.SetDefaults()

	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     cfg.VCenterHost,
		Username: cfg.VCenterUsername,
		Password: cfg.VCenterPassword,
		Port:     cfg.VCenterPort,
		Insecure: cfg.VCenterInsecure,
	})
	if err != nil {
		return false, fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() { _ = vclient.Disconnect() }()

	vmObj, err := vclient.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil {
		return false, fmt.Errorf("failed to check node existence: %w", err)
	}
	return vmObj != nil, nil
}

// DeleteNode deletes an existing node by cfg.Name from vCenter.
// It is idempotent: if the node does not exist, it returns nil.
func DeleteNode(ctx context.Context, cfg *VMConfig) error {
	cfg.SetDefaults()

	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     cfg.VCenterHost,
		Username: cfg.VCenterUsername,
		Password: cfg.VCenterPassword,
		Port:     cfg.VCenterPort,
		Insecure: cfg.VCenterInsecure,
	})
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() { _ = vclient.Disconnect() }()

	vmObj, err := vclient.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil {
		return fmt.Errorf("failed to find node: %w", err)
	}
	if vmObj == nil {
		return nil
	}

	state, err := vmObj.PowerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get node power state: %w", err)
	}
	if state == types.VirtualMachinePowerStatePoweredOn {
		task, err := vmObj.PowerOff(ctx)
		if err != nil {
			return fmt.Errorf("failed to power off node: %w", err)
		}
		if err := task.Wait(ctx); err != nil {
			return fmt.Errorf("failed waiting for node power off: %w", err)
		}
	}

	task, err := vmObj.Destroy(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete node: %w", err)
	}
	if err := task.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for node deletion: %w", err)
	}
	return nil
}

// RecreateNode deletes the existing node (if present) and creates it again.
func RecreateNode(ctx context.Context, cfg *VMConfig) (*VM, error) {
	return RecreateNodeWithLogger(ctx, cfg, defaultLogger)
}

// RecreateNodeWithLogger deletes the existing node (if present) and creates it again.
func RecreateNodeWithLogger(ctx context.Context, cfg *VMConfig, logger *slog.Logger) (*VM, error) {
	if err := DeleteNode(ctx, cfg); err != nil {
		return nil, err
	}
	return CreateNodeWithLogger(ctx, cfg, logger)
}
