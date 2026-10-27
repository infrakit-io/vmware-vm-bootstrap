package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	"github.com/spf13/cobra"
	"github.com/vmware/govmomi/object"
	"gopkg.in/yaml.v3"
)

var diskCmd = &cobra.Command{
	Use:           "disk",
	Short:         "Add a disk to a (running) VM",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var diskAddCmd = &cobra.Command{
	Use:           "add",
	Short:         "Add a thin disk on a datastore to the VM's SCSI controller (no power operation)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		sizeGB, _ := cmd.Flags().GetInt64("size-gb")
		datastore, _ := cmd.Flags().GetString("datastore")
		unitStr, _ := cmd.Flags().GetString("controller-unit")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if cfgPath == "" || datastore == "" || sizeGB <= 0 {
			return fmt.Errorf("--config, --datastore and --size-gb (> 0) are required")
		}
		unit := int32(-1)
		if unitStr != "auto" {
			n, err := strconv.Atoi(unitStr)
			if err != nil || n < 0 || n > 15 || n == 7 {
				return fmt.Errorf("--controller-unit must be auto or 0-15 (not 7)")
			}
			unit = int32(n)
		}
		return runDiskAdd(cfgPath, datastore, sizeGB, unit, dryRun)
	},
}

func runDiskAdd(vmConfigPath, datastore string, sizeGB int64, unit int32, dryRun bool) error {
	vcCfg, err := loadVCenterConfig(vcenterConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load vCenter config: %w", err)
	}
	vmOut, err := sopsDecrypt(vmConfigPath)
	if err != nil {
		return err
	}
	var vmFile vmFileConfig
	if err := yaml.Unmarshal(vmOut, &vmFile); err != nil {
		return fmt.Errorf("failed to parse VM config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vcCfg.VCenter.Host,
		Username: vcCfg.VCenter.Username,
		Password: vcCfg.VCenter.Password,
		Port:     vcCfg.VCenter.Port,
		Insecure: vcCfg.VCenter.Insecure,
	})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = vclient.Disconnect() }()

	vmObj, err := vclient.FindVM(vcCfg.VCenter.Datacenter, vmFile.VM.Name)
	if err != nil || vmObj == nil {
		return fmt.Errorf("VM %q not found in vCenter: %v", vmFile.VM.Name, err)
	}
	ds, err := vclient.FindDatastore(vcCfg.VCenter.Datacenter, datastore)
	if err != nil {
		return err
	}
	reserved := 1 // boot disk
	if vmFile.VM.DataDiskSizeGB > 0 {
		reserved++
	}
	return applyDiskAdd(ctx, vmFile.VM.Name, vmObj, ds, datastore, sizeGB, unit, reserved, dryRun)
}

func applyDiskAdd(ctx context.Context, name string, vmObj *object.VirtualMachine, ds *object.Datastore, dsName string, sizeGB int64, unit int32, reserved int, dryRun bool) error {
	res, err := vm.EnsureExtraDisk(ctx, vm.NewCreator(ctx), vmObj, ds, dsName, sizeGB, unit, reserved, dryRun)
	if err != nil {
		return err
	}
	switch {
	case !res.Changed:
		fmt.Printf("  %s: %d GB disk on %q already present (SCSI unit %d), no change.\n", name, sizeGB, dsName, res.Unit)
	case dryRun:
		fmt.Printf("  [dry-run] %s: would add disk (controller key %d, unit %d, datastore %q, %d GB, thin).\n", name, res.ControllerKey, res.Unit, dsName, sizeGB)
	default:
		fmt.Printf("  %s: added %d GB thin disk on %q (controller key %d, unit %d).\n", name, sizeGB, dsName, res.ControllerKey, res.Unit)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(diskCmd)
	diskAddCmd.Flags().String("config", "", "Path to VM config file (SOPS encrypted)")
	diskAddCmd.Flags().Int64("size-gb", 0, "Disk size in GB")
	diskAddCmd.Flags().String("datastore", "", "Datastore for the new disk")
	diskAddCmd.Flags().String("controller-unit", "auto", "SCSI unit number on the controller (auto or 0-15, not 7)")
	diskAddCmd.Flags().Bool("dry-run", false, "Print the plan only, change nothing")
	diskCmd.AddCommand(diskAddCmd)
}
