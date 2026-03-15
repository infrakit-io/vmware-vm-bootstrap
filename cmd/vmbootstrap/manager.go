package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wizard "github.com/infrakit-io/cli-wizard-core"
	"gopkg.in/yaml.v3"
)

type menuItem struct {
	label  string
	action func() error
}

func menuLabel(tag, text string) string {
	return wizard.FormatMenuLabel(tag, text, 13)
}

func colorizeMenuLabel(label, color string) string {
	return wizard.Colorize(label, color)
}

func runManager() error {
	for {
		warnings := checkRequiredFiles()

		fmt.Println()
		fmt.Println("\033[1mvmbootstrap\033[0m — Config Manager")
		fmt.Println(strings.Repeat("─", 50))
		if len(warnings) > 0 {
			for _, w := range warnings {
				fmt.Printf("  \033[33m⚠  missing required: %s\033[0m\n", w)
			}
			fmt.Println()
		}

		items := buildMenuItems()

		var labels []string
		for _, it := range items {
			labels = append(labels, it.label)
		}

		choice := sel.Select(labels, "", "Select:")
		if sel.WasInterrupted() {
			return wizard.ErrInterrupted
		}

		for _, it := range items {
			if it.label == choice {
				if it.action == nil {
					return nil // Exit
				}
				fmt.Println()
				if err := it.action(); err != nil {
					fmt.Printf("\n\033[31m✗ Error: %v\033[0m\n", err)
					fmt.Print("\nPress Enter to continue...")
					_, _ = stdinReader.ReadString('\n')
				}
				break
			}
		}
	}
}

func buildMenuItems() []menuItem {
	var items []menuItem

	vcenterPath := resolveConfigPath("configs/vcenter.sops.yaml")
	_, vcenterErr := os.Stat(vcenterPath)
	vcenterExists := vcenterErr == nil

	vcenterTag := "vcenter"
	vcenterText := "Manage vcenter.sops.yaml"
	if !vcenterExists {
		vcenterTag = "+vcenter"
		vcenterText = "Create vcenter.sops.yaml"
	}
	items = append(items, menuItem{
		label: menuLabel(vcenterTag, vcenterText),
		action: func() error {
			if _, err := os.Stat(vcenterPath); err == nil {
				return editVCenterConfig(vcenterPath)
			}
			return createVCenterConfig(vcenterPath)
		},
	})

	vmFiles, _ := filepath.Glob(resolveConfigPath("configs/vm.*.sops.yaml"))
	for _, path := range vmFiles {
		p := path
		base := filepath.Base(p)
		items = append(items, menuItem{
			label:  menuLabel("vm", "Edit "+base),
			action: func() error { return editVMConfig(p) },
		})
	}

	schematicTag := "schematic"
	schematicText := "Manage talos.schematics.sops.yaml"
	if _, err := os.Stat(talosSchematicsConfigFile); err != nil {
		schematicTag = "+schematic"
		schematicText = "Create talos.schematics.sops.yaml"
	}
	items = append(items, menuItem{label: menuLabel(schematicTag, schematicText), action: runTalosConfigWizard})
	drafts := listDrafts(true)
	for _, d := range drafts {
		draft := d
		items = append(items, menuItem{
			label:  colorizeMenuLabel(menuLabel("draft", "Resume "+draft.label), "\033[33m"),
			action: func() error { return resumeDraft(draft) },
		})
		items = append(items, menuItem{
			label:  colorizeMenuLabel(menuLabel("draft", "Delete "+draft.label), "\033[31m"),
			action: func() error { return deleteDraft(draft.path) },
		})
	}

	if vcenterExists {
		items = append(items, menuItem{label: menuLabel("+vm", "Create new VM"), action: runCreateWizard})
	}
	items = append(items, menuItem{label: menuLabel("", "Exit"), action: nil})

	return items
}

type draftInfo struct {
	path       string
	targetPath string
	kind       string
	label      string
}

func listDrafts(all bool) []draftInfo {
	matches, _ := filepath.Glob(filepath.Join("tmp", "*.draft.*.yaml"))
	type item struct {
		info    draftInfo
		modTime time.Time
	}
	var items []item
	for _, p := range matches {
		base := filepath.Base(p)
		token := strings.Split(base, ".draft.")[0]
		// token uses "__" as path separator (wizard.DraftTargetToken format).
		// Convert back to a native path; fall back to configs/<token> for old-style drafts.
		var targetPath string
		if strings.Contains(token, "__") {
			targetPath = strings.ReplaceAll(token, "__", string(filepath.Separator))
		} else {
			targetPath = filepath.Join("configs", token)
		}
		targetBase := filepath.Base(targetPath)
		kind := detectDraftKind(p, targetBase)
		fi, _ := os.Stat(p)
		mt := time.Time{}
		if fi != nil {
			mt = fi.ModTime()
		}
		items = append(items, item{
			info: draftInfo{
				path:       p,
				targetPath: targetPath,
				kind:       kind,
				label:      targetBase,
			},
			modTime: mt,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].modTime.After(items[j].modTime) })

	var drafts []draftInfo
	if all {
		for _, it := range items {
			if it.info.kind == "unknown" {
				continue
			}
			it.info.label = it.info.label + " (" + filepath.Base(it.info.path) + ")"
			drafts = append(drafts, it.info)
		}
		return drafts
	}

	seen := make(map[string]bool)
	for _, it := range items {
		key := it.info.label
		if seen[key] {
			continue
		}
		seen[key] = true
		drafts = append(drafts, it.info)
	}
	return drafts
}

func detectDraftKind(draftPath, targetBase string) string {
	// Content-based detection only: no filename constraints.
	var data map[string]any
	raw, err := os.ReadFile(draftPath)
	if err == nil {
		if err := yaml.Unmarshal(raw, &data); err == nil {
			if _, ok := data["vm"]; ok {
				return "vm"
			}
			if _, ok := data["vcenter"]; ok {
				return "vcenter"
			}
			if _, ok := data["talos"]; ok {
				return "talos_schematics"
			}
		}
	}
	return "unknown"
}

func deleteDraft(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("\n\033[32m✓ Draft deleted: %s\033[0m\n", filepath.Base(path))
	return nil
}

func resumeDraft(d draftInfo) error {
	switch d.kind {
	case "vcenter":
		return createVCenterConfigWithDraft(d.targetPath, d.path)
	case "vm":
		return runCreateWizardWithDraft(d.targetPath, d.path)
	case "talos_schematics":
		return runTalosConfigWizardWithDraft(d.path)
	default:
		return fmt.Errorf("unknown draft type: %s", d.label)
	}
}

func runBootstrapSelector() error {
	selectedPath, selectedLabel, err := selectVMConfig("\033[1mvmbootstrap\033[0m — Bootstrap VM", "Select VM config to bootstrap:")
	if err != nil {
		return err
	}
	if selectedPath == "" {
		return nil
	}
	if !readYesNo(fmt.Sprintf("Bootstrap %s?", selectedLabel), true) {
		fmt.Println("  Cancelled.")
		return nil
	}
	return bootstrapVM(selectedPath, bootstrapResultPath)
}

func selectVMConfig(title, prompt string) (string, string, error) {
	vmFiles, _ := filepath.Glob(resolveConfigPath("configs/vm.*.sops.yaml"))
	if len(vmFiles) == 0 {
		fmt.Println("\n  No VM configs found in configs/vm.*.sops.yaml")
		fmt.Println("  Run: make config → Create new VM")
		return "", "", nil
	}

	labels := make([]string, len(vmFiles))
	for i, f := range vmFiles {
		labels[i] = filepath.Base(f)
	}
	options := append([]string{}, labels...)
	options = append(options, "Exit")

	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("─", 50))
	selected := sel.Select(options, labels[0], prompt)
	if sel.WasInterrupted() {
		return "", "", nil
	}
	if selected == "Exit" {
		fmt.Println()
		return "", "", nil
	}
	fmt.Println()

	var selectedPath string
	for i, label := range labels {
		if label == selected {
			selectedPath = vmFiles[i]
			break
		}
	}
	return selectedPath, selected, nil
}

func checkRequiredFiles() []string {
	var missing []string
	for _, f := range []string{"vcenter.sops.yaml", "defaults.yaml"} {
		if _, err := os.Stat(filepath.Join("configs", f)); os.IsNotExist(err) {
			missing = append(missing, f)
		}
	}
	return missing
}
