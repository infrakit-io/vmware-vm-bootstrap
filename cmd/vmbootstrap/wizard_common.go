package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	wizard "github.com/infrakit-io/cli-wizard-core"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"gopkg.in/yaml.v3"
)

type WizardSession = wizard.Session
type WizardStep = wizard.Step

func NewWizardSession(targetPath, draftPath string, state any, isEmpty func() bool) *WizardSession {
	return wizard.NewSession(
		targetPath,
		draftPath,
		state,
		isEmpty,
		wizard.LoadDraftYAML,
		startYAMLDraftHandler,
		wizard.CleanupDrafts,
	)
}

// VCenterCatalog is a reusable snapshot of vCenter pick-list resources.
type VCenterCatalog struct {
	Datastores []vcenter.DatastoreInfo
	Networks   []vcenter.NetworkInfo
	Folders    []vcenter.FolderInfo
	Pools      []vcenter.ResourcePoolInfo
}

type VCenterResources struct {
	Datastores []vcenter.DatastoreInfo
	Networks   []vcenter.NetworkInfo
	Folders    []vcenter.FolderInfo
	Pools      []vcenter.ResourcePoolInfo

	DatastoresErr error
	NetworksErr   error
	FoldersErr    error
	PoolsErr      error
}

func fetchVCenterResources(vcCfg *vcenterFileConfig, timeout time.Duration) (*VCenterResources, error) {
	if vcCfg == nil {
		return nil, fmt.Errorf("vcenter config is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vcCfg.VCenter.Host,
		Username: vcCfg.VCenter.Username,
		Password: vcCfg.VCenter.Password,
		Port:     vcCfg.VCenter.Port,
		Insecure: vcCfg.VCenter.Insecure,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = vclient.Disconnect() }()

	ds, dsErr := vclient.ListDatastores(vcCfg.VCenter.Datacenter)
	nets, netsErr := vclient.ListNetworks(vcCfg.VCenter.Datacenter)
	fl, flErr := vclient.ListFolders(vcCfg.VCenter.Datacenter)
	pl, plErr := vclient.ListResourcePools(vcCfg.VCenter.Datacenter)

	return &VCenterResources{
		Datastores:    ds,
		Networks:      nets,
		Folders:       fl,
		Pools:         pl,
		DatastoresErr: dsErr,
		NetworksErr:   netsErr,
		FoldersErr:    flErr,
		PoolsErr:      plErr,
	}, nil
}

func fetchVCenterCatalog(vcCfg *vcenterFileConfig, timeout time.Duration) (*VCenterCatalog, error) {
	rs, err := fetchVCenterResources(vcCfg, timeout)
	if err != nil {
		return nil, err
	}

	return &VCenterCatalog{
		Datastores: rs.Datastores,
		Networks:   rs.Networks,
		Folders:    rs.Folders,
		Pools:      rs.Pools,
	}, nil
}

func pickDatastoreFromCatalogWithPrompt(cat *VCenterCatalog, current, manualPrompt, listHeader string) string {
	if cat == nil || len(cat.Datastores) == 0 {
		return readLine(manualPrompt, current)
	}
	if strings.TrimSpace(listHeader) != "" {
		fmt.Printf("  %s\n", listHeader)
	}
	return selectISODatastore(cat.Datastores, current)
}

func pickNetworkFromCatalogWithPrompt(cat *VCenterCatalog, current, manualPrompt, listLabel string) string {
	if cat == nil || len(cat.Networks) == 0 {
		return readLine(manualPrompt, current)
	}
	return sel.Select(vcenterNetworkLeafNames(cat.Networks), current, listLabel)
}

func pickFolderFromCatalogWithPrompt(cat *VCenterCatalog, current, manualPrompt, listLabel string) string {
	if cat == nil || len(cat.Folders) == 0 {
		return readLine(manualPrompt, current)
	}
	return selectFolder(cat.Folders, current, listLabel)
}

func pickResourcePoolFromCatalogWithPrompt(cat *VCenterCatalog, current, manualPrompt, listLabel string) string {
	if cat == nil || len(cat.Pools) == 0 {
		return readLine(manualPrompt, current)
	}
	return selectResourcePool(cat.Pools, current, listLabel)
}

func vcenterNetworkLeafNames(networks []vcenter.NetworkInfo) []string {
	out := make([]string, 0, len(networks))
	for _, n := range networks {
		parts := strings.Split(n.Name, "/")
		out = append(out, parts[len(parts)-1])
	}
	return out
}

func catalogIfReady(cat *VCenterCatalog, err error) *VCenterCatalog {
	if err != nil {
		return nil
	}
	return cat
}

// startYAMLDraftHandler standardizes Ctrl+C draft-save behavior for wizard flows.
// It stores plaintext YAML draft to tmp/ and restores global signal handler.
func startYAMLDraftHandler(targetPath, draftPath string, state any, isEmpty func() bool) func() {
	return startDraftInterruptHandler(targetPath, draftPath, func() ([]byte, bool) {
		if isEmpty != nil && isEmpty() {
			return nil, false
		}
		data, err := yaml.Marshal(state)
		if err != nil {
			return nil, false
		}
		return data, true
	})
}
