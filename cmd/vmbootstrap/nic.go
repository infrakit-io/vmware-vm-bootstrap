package main

import (
	"context"
	"fmt"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	"github.com/spf13/cobra"
	"github.com/vmware/govmomi/object"
	"gopkg.in/yaml.v3"
)

var nicCmd = &cobra.Command{
	Use:           "nic",
	Short:         "Connect or disconnect a VM NIC by portgroup",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func newNICSubcommand(use, short string, connected bool) *cobra.Command {
	return &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			network, _ := cmd.Flags().GetString("network")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if cfgPath == "" || network == "" {
				return fmt.Errorf("--config and --network are required")
			}
			return runNICSet(cfgPath, network, connected, dryRun)
		},
	}
}

func runNICSet(vmConfigPath, network string, connected, dryRun bool) error {
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
	return applyNICState(ctx, vmFile.VM.Name, vmObj, network, connected, dryRun)
}

func applyNICState(ctx context.Context, name string, vmObj *object.VirtualMachine, network string, connected, dryRun bool) error {
	verb := "connect"
	if !connected {
		verb = "disconnect"
	}
	res, err := vm.SetNICConnected(ctx, vmObj, network, connected, dryRun)
	if err != nil {
		return err
	}
	switch {
	case !res.Changed:
		fmt.Printf("  %s: NIC %s (%s) on %q already in requested state, no change.\n", name, res.Label, res.MAC, network)
	case dryRun:
		fmt.Printf("  [dry-run] %s: would %s NIC %s (%s) on %q (Connected, StartConnected = %v).\n", name, verb, res.Label, res.MAC, network, connected)
	default:
		fmt.Printf("  %s: NIC %s (%s) on %q %sed.\n", name, res.Label, res.MAC, network, verb)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(nicCmd)
	connectCmd := newNICSubcommand("connect", "Connect (and start-connected) the NIC on a portgroup", true)
	disconnectCmd := newNICSubcommand("disconnect", "Disconnect the NIC on a portgroup (rollback)", false)
	for _, c := range []*cobra.Command{connectCmd, disconnectCmd} {
		c.Flags().String("config", "", "Path to VM config file (SOPS encrypted)")
		c.Flags().String("network", "", "Portgroup name of the NIC to change")
		c.Flags().Bool("dry-run", false, "Print the plan only, change nothing")
		nicCmd.AddCommand(c)
	}
}
