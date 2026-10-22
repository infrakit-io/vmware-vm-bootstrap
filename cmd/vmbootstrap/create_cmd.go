package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a VM from a config file, non-interactively",
	Long: `Create a VM from a vm.*.sops.yaml config without any picker.

Uses the same config loading and bootstrap code path as 'run'.
If the VM already exists the command fails and changes nothing (it never
offers delete/recreate). On failure the VM is powered off and kept unless
cleanup_on_error is set in the config.
Without --yes it asks for confirmation once; with --yes it never prompts.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if cfgPath == "" {
			return fmt.Errorf("--config is required")
		}
		vcCfg, err := loadVCenterConfig(vcenterConfigFile)
		if err != nil {
			return fmt.Errorf("failed to load vCenter config: %w", err)
		}
		vmOut, err := sopsDecrypt(cfgPath)
		if err != nil {
			return err
		}
		var vmFile vmFileConfig
		if err := yaml.Unmarshal(vmOut, &vmFile); err != nil {
			return fmt.Errorf("failed to parse VM config: %w", err)
		}
		return runCreate(vcCfg, &vmFile, createOptions{Yes: yes, DryRun: dryRun}, defaultCreateDeps(os.Stdout))
	},
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().String("config", "", "Path to VM config file (SOPS encrypted)")
	createCmd.Flags().Bool("yes", false, "Do not prompt for confirmation")
	createCmd.Flags().Bool("dry-run", false, "Print the resolved plan and exit (read-only vCenter lookup only)")
}

type createOptions struct {
	Yes    bool
	DryRun bool
}

// createDeps are the side effects of create, injectable for tests.
type createDeps struct {
	out     io.Writer
	confirm func(msg string) bool
	exists  func(cfg *bootstrap.VMConfig) (bool, error)
	run     func(ctx context.Context, cfg *bootstrap.VMConfig) (*bootstrap.VM, error)
}

func defaultCreateDeps(out io.Writer) createDeps {
	return createDeps{
		out:     out,
		confirm: func(msg string) bool { return readYesNo(msg, false) },
		exists:  vmExists,
		run: func(ctx context.Context, cfg *bootstrap.VMConfig) (*bootstrap.VM, error) {
			if cfg.EffectiveProfile() == "talos" {
				return bootstrap.CreateTalosNodeFromOVA(ctx, cfg, getLogger())
			}
			return bootstrap.BootstrapWithLogger(ctx, cfg, getLogger())
		},
	}
}

func runCreate(vcCfg *vcenterFileConfig, vmFile *vmFileConfig, opts createOptions, d createDeps) error {
	built, err := buildVMConfig(vcCfg, vmFile, "")
	if err != nil {
		return err
	}
	cfg := built.cfg
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid VM config: %w", err)
	}

	exists, err := d.exists(cfg)
	if err != nil {
		return fmt.Errorf("cannot check whether VM %q exists: %w", cfg.Name, err)
	}
	if exists {
		return fmt.Errorf("VM %q already exists in vCenter; nothing was changed", cfg.Name)
	}

	printCreatePlan(d.out, cfg)
	if opts.DryRun {
		fmt.Fprintln(d.out, "\n[dry-run] nothing was created.")
		return nil
	}
	if !opts.Yes && !d.confirm(fmt.Sprintf("Create VM %s?", cfg.Name)) {
		fmt.Fprintln(d.out, "  Cancelled.")
		return nil
	}

	timeoutMinutes := vmFile.VM.TimeoutMinutes
	if timeoutMinutes == 0 {
		timeoutMinutes = 45
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMinutes)*time.Minute+configs.Defaults.Timeouts.Download())
	defer cancel()
	// Take over Ctrl+C from the global handler (which exits 0) so the bootstrap
	// failure handler runs: power off, keep the VM, remove the NoCloud ISO.
	signal.Stop(mainSigCh)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer func() {
		signal.Stop(sigCh)
		signal.Notify(mainSigCh, os.Interrupt)
	}()
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	vm, err := d.run(ctx, cfg)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			fmt.Fprintln(d.out, "\n\033[33m⚠ Interrupted\033[0m")
		}
		var keptErr *bootstrap.KeptVMError
		if errors.As(err, &keptErr) {
			fmt.Fprintln(d.out)
			fmt.Fprintln(d.out, keptVMMessage(keptErr))
		}
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	fmt.Fprintf(d.out, "\n\033[32m✓ VM created:\033[0m %s (%s)\n", vm.Name, vm.IPAddress)
	return nil
}

func printCreatePlan(w io.Writer, cfg *bootstrap.VMConfig) {
	fmt.Fprintf(w, "Plan for %s\n", cfg.Name)
	fmt.Fprintf(w, "  vCenter:    %s (datacenter %s)\n", cfg.VCenterHost, cfg.Datacenter)
	fmt.Fprintf(w, "  Profile/OS: %s %s\n", cfg.EffectiveProfile(), cfg.EffectiveOSVersion())
	fmt.Fprintf(w, "  Folder:     %s\n", cfg.Folder)
	fmt.Fprintf(w, "  Pool:       %s\n", cfg.ResourcePool)
	fmt.Fprintf(w, "  CPU/RAM:    %d vCPU / %d MB (hot-add cpu=%s mem=%s)\n", cfg.CPUs, cfg.MemoryMB, boolPtrStr(cfg.CPUHotAdd), boolPtrStr(cfg.MemoryHotAdd))
	fmt.Fprintf(w, "  OS disk:    %d GB on %s\n", cfg.DiskSizeGB, cfg.Datastore)
	if cfg.DataDiskSizeGB != nil {
		ds := cfg.DataDiskDatastore
		if ds == "" {
			ds = cfg.Datastore + " (same as OS disk)"
		}
		fmt.Fprintf(w, "  Data disk:  %d GB on %s, mount %s\n", *cfg.DataDiskSizeGB, ds, cfg.DataDiskMountPath)
	}
	if cfg.SwapSizeGB != nil {
		fmt.Fprintf(w, "  Swap:       %d GB\n", *cfg.SwapSizeGB)
	}
	fmt.Fprintf(w, "  NIC 1:      %s %s/%s gw %s (connected)\n", cfg.NetworkName, cfg.IPAddress, cfg.Netmask, cfg.Gateway)
	for i, n := range cfg.ExtraNICs {
		state := "connected"
		if !n.Connected {
			state = "DISCONNECTED"
		}
		fmt.Fprintf(w, "  NIC %d:      %s %s/%s, %s, guest-control=%v\n", i+2, n.Network, n.IP, n.Netmask, state, n.AllowGuestControl)
	}
	if len(cfg.Tags) > 0 {
		var ts []string
		for _, t := range cfg.Tags {
			ts = append(ts, t.Category+"/"+t.Tag)
		}
		fmt.Fprintf(w, "  Tags:       %s\n", strings.Join(ts, ", "))
	}
	fmt.Fprintf(w, "  On failure: %s\n", map[bool]string{true: "DESTROY the VM (cleanup_on_error)", false: "power off and keep the VM"}[cfg.CleanupOnError])
}

func boolPtrStr(b *bool) string {
	if b == nil {
		return "unchanged"
	}
	return fmt.Sprint(*b)
}
