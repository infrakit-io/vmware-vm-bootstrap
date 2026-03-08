package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	contentlibrary "github.com/infrakit-io/vmware-content-library-core"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
)

type ovfImportSpec struct {
	Name           string `json:"Name,omitempty"`
	NetworkMapping []struct {
		Name    string `json:"Name,omitempty"`
		Network string `json:"Network,omitempty"`
	} `json:"NetworkMapping,omitempty"`
}

func normalizeTalosVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func talosOVAURL(version, schematicID string) string {
	return fmt.Sprintf("https://factory.talos.dev/image/%s/%s/vmware-amd64.ova",
		strings.TrimSpace(schematicID), normalizeTalosVersion(version))
}

func govcEnv(cfg *VMConfig) []string {
	url := cfg.VCenterHost
	if !strings.Contains(url, "://") {
		url = "https://" + url + "/sdk"
	}
	return append(os.Environ(),
		"GOVC_URL="+url,
		"GOVC_USERNAME="+cfg.VCenterUsername,
		"GOVC_PASSWORD="+cfg.VCenterPassword,
		"GOVC_DATACENTER="+cfg.Datacenter,
		fmt.Sprintf("GOVC_INSECURE=%t", cfg.VCenterInsecure),
	)
}

func runGovc(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "govc", args...)
	cmd.Env = env
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

var talosLibraryItemSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func talosLibraryItemName(version, schematicID string) string {
	v := strings.TrimPrefix(normalizeTalosVersion(version), "v")
	if v == "" {
		v = "latest"
	}
	s := strings.TrimSpace(schematicID)
	if len(s) > 12 {
		s = s[:12]
	}
	if s == "" {
		s = "default"
	}
	name := fmt.Sprintf("talos-%s-%s", v, s)
	return talosLibraryItemSanitizer.ReplaceAllString(name, "-")
}

func govcErrContains(err error, needle string) bool {
	if err == nil || strings.TrimSpace(needle) == "" {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), strings.ToLower(needle))
}

// CreateTalosNodeFromOVA deploys a Talos VMware OVA and powers the VM on.
func CreateTalosNodeFromOVA(ctx context.Context, cfg *VMConfig, logger *slog.Logger) (*VM, error) {
	if logger == nil {
		logger = defaultLogger
	}
	cfg.SetDefaults()
	if cfg.Profile != "talos" {
		return nil, fmt.Errorf("CreateTalosNodeFromOVA requires Profile=talos")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if cfg.Datacenter == "" || cfg.Datastore == "" {
		return nil, fmt.Errorf("datacenter and datastore are required")
	}

	version := normalizeTalosVersion(cfg.EffectiveTalosVersion())
	if version == "" {
		return nil, fmt.Errorf("Profiles.Talos.Version is required")
	}

	schematicID := strings.TrimSpace(cfg.Profiles.Talos.SchematicID)
	if schematicID == "" {
		return nil, fmt.Errorf("Profiles.Talos.SchematicID is required")
	}
	ovaURL := talosOVAURL(version, schematicID)
	logger.Info("Deploying Talos OVA", "url", ovaURL, "version", version, "schematic_id", schematicID)

	env := govcEnv(cfg)
	libClient := contentlibrary.NewClient(contentlibrary.GovcRunner{Env: env})
	libraryTarget := strings.TrimSpace(cfg.ContentLibraryID)
	libraryName := libraryTarget
	if libraryTarget == "" {
		libraryName = strings.TrimSpace(cfg.ContentLibrary)
		if libraryName == "" {
			libraryName = "talos-images"
		}
		libRef, err := libClient.EnsureLibrary(ctx, libraryName)
		if err != nil {
			return nil, fmt.Errorf("ensure content library %q: %w", libraryName, err)
		}
		libraryTarget = libRef.Target
	}
	itemName := talosLibraryItemName(version, schematicID)
	logger.Info("Talos content library target", "library", libraryName, "item", itemName)
	if err := libClient.EnsureItemFromURL(ctx, libraryTarget, itemName, ovaURL); err != nil {
		return nil, fmt.Errorf("ensure library item: %w", err)
	}

	specRaw, err := runGovc(ctx, env, "import.spec", ovaURL)
	if err != nil {
		return nil, fmt.Errorf("govc import.spec failed: %w", err)
	}
	var spec ovfImportSpec
	if err := json.Unmarshal(specRaw, &spec); err != nil {
		return nil, fmt.Errorf("parse import spec: %w", err)
	}
	spec.Name = cfg.Name
	if cfg.NetworkName != "" {
		for i := range spec.NetworkMapping {
			spec.NetworkMapping[i].Network = cfg.NetworkName
		}
	}

	if err := os.MkdirAll("tmp", 0o755); err != nil {
		return nil, err
	}
	specPath := filepath.Join("tmp", fmt.Sprintf("ova-import-%s.json", cfg.Name))
	specBytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal import spec: %w", err)
	}
	if err := os.WriteFile(specPath, specBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write import spec: %w", err)
	}
	defer func() { _ = os.Remove(specPath) }()

	deployOpt := contentlibrary.DeployOptions{
		Datacenter:   cfg.Datacenter,
		Datastore:    cfg.Datastore,
		Folder:       cfg.Folder,
		ResourcePool: cfg.ResourcePool,
		OptionsPath:  specPath,
		ItemPath:     contentlibrary.ItemPath(libraryTarget, itemName),
		VMName:       cfg.Name,
	}
	if err := libClient.DeployItem(ctx, deployOpt); err != nil {
		// Recover once from broken library item state (e.g. partial failed import).
		if govcErrContains(err, "invalid_library_item") || govcErrContains(err, "not an OVF") {
			libClient.RemoveItem(ctx, libraryTarget, itemName)
			if impErr := libClient.ImportItemFromURL(ctx, libraryTarget, itemName, ovaURL); impErr == nil {
				if retryErr := libClient.DeployItem(ctx, deployOpt); retryErr == nil {
					goto deployed
				} else {
					return nil, fmt.Errorf("library deploy failed after item reimport: %w", retryErr)
				}
			} else {
				return nil, fmt.Errorf("recover invalid library item failed: %w", impErr)
			}
		}
		return nil, err
	}
deployed:

	if _, err := runGovc(ctx, env, "vm.power", "-on", cfg.Name); err != nil {
		return nil, fmt.Errorf("failed to power on Talos VM: %w", err)
	}

	vc, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     cfg.VCenterHost,
		Port:     cfg.VCenterPort,
		Username: cfg.VCenterUsername,
		Password: cfg.VCenterPassword,
		Insecure: cfg.VCenterInsecure,
	})
	if err != nil {
		return nil, fmt.Errorf("vCenter connection failed after OVA deploy: %w", err)
	}
	defer func() { _ = vc.Disconnect() }()

	vmObj, err := vc.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to locate created Talos VM: %w", err)
	}
	if vmObj == nil {
		return nil, fmt.Errorf("created Talos VM not found: %s", cfg.Name)
	}

	managed := vmObj.Reference()
	return &VM{
		Name:            cfg.Name,
		IPAddress:       cfg.IPAddress,
		ManagedObject:   managed,
		SSHReady:        false,
		Hostname:        cfg.Name,
		VCenterHost:     cfg.VCenterHost,
		VCenterPort:     cfg.VCenterPort,
		VCenterUser:     cfg.VCenterUsername,
		VCenterPass:     cfg.VCenterPassword,
		VCenterInsecure: cfg.VCenterInsecure,
	}, nil
}
