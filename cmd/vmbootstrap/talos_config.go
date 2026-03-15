package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	wizard "github.com/infrakit-io/cli-wizard-core"
	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"gopkg.in/yaml.v3"
)

var talosSchematicsConfigFile = resolveConfigPath("configs/talos.schematics.sops.yaml")

type talosSchematicsFile struct {
	Talos talosSchematicsSection `yaml:"talos"`
}

type talosSchematicsSection struct {
	FactoryURL string                `yaml:"factory_url,omitempty"`
	Default    string                `yaml:"default,omitempty"`
	Schematics []talosSchematicEntry `yaml:"schematics,omitempty"`
}

type talosSchematicEntry struct {
	Name       string   `yaml:"name"`
	ID         string   `yaml:"id"`
	Extensions []string `yaml:"extensions,omitempty"`
	CreatedAt  string   `yaml:"created_at,omitempty"`
}

func loadTalosSchematics(path string) (*talosSchematicsFile, error) {
	var cfg talosSchematicsFile
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg.Talos.FactoryURL = configs.TalosExtensions.FactoryURL
		return &cfg, nil
	}
	data, err := sopsDecrypt(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if strings.TrimSpace(cfg.Talos.FactoryURL) == "" {
		cfg.Talos.FactoryURL = configs.TalosExtensions.FactoryURL
	}
	return &cfg, nil
}

func saveTalosSchematics(path string, cfg *talosSchematicsFile) error {
	if cfg == nil {
		return fmt.Errorf("nil talos schematic config")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal talos schematic config: %w", err)
	}
	if err := sopsEncrypt(path, data); err != nil {
		return err
	}
	return nil
}

func runTalosConfigWizard() error {
	return runTalosConfigWizardWithDraft("")
}

func runTalosConfigWizardWithDraft(draftPath string) error {
	fmt.Printf("\n\033[1mTalos\033[0m — Schematic Manager\n")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println()

	if draftPath == "" {
		draftPath = wizard.LatestDraftForTarget(talosSchematicsConfigFile)
	}

	cfg := &talosSchematicsFile{}
	if loaded, err := wizard.LoadDraftYAML(draftPath, cfg); err == nil && loaded {
		fmt.Printf("\033[33m⚠ Resuming draft: %s\033[0m\n\n", filepath.Base(draftPath))
	} else {
		loadedCfg, err := loadTalosSchematics(talosSchematicsConfigFile)
		if err != nil {
			return err
		}
		cfg = loadedCfg
	}

	if strings.TrimSpace(cfg.Talos.FactoryURL) == "" {
		cfg.Talos.FactoryURL = configs.TalosExtensions.FactoryURL
	}

	session := NewWizardSession(talosSchematicsConfigFile, draftPath, cfg, func() bool {
		return strings.TrimSpace(cfg.Talos.FactoryURL) == "" &&
			strings.TrimSpace(cfg.Talos.Default) == "" &&
			len(cfg.Talos.Schematics) == 0
	})
	session.Start()
	defer session.Stop()

	defaultName := strings.TrimSpace(cfg.Talos.Default)
	if defaultName == "" {
		defaultName = "VMware"
	}

	var (
		name       string
		factoryURL string
		selected   []string
		cancelled  bool
	)
	if err := wizard.DefaultRunSteps([]WizardStep{
		{
			Name: "Schematic Metadata",
			Run: func() error {
				name = strings.TrimSpace(readLine("Schematic name", defaultName))
				if wasPromptInterrupted() {
					fmt.Println("  Cancelled.")
					cancelled = true
					return nil
				}
				if name == "" {
					fmt.Println("  Schematic name is required")
					cancelled = true
					return nil
				}
				if existingName, exists := findSchematicName(cfg.Talos.Schematics, name); exists {
					if !readYesNo(
						fmt.Sprintf("Schematic '%s' already exists. Overwrite it?", existingName),
						false,
					) {
						fmt.Println("  Cancelled.")
						cancelled = true
						return nil
					}
				}
				factoryURL = strings.TrimSpace(readLine("Image Factory URL", cfg.Talos.FactoryURL))
				if wasPromptInterrupted() {
					fmt.Println("  Cancelled.")
					cancelled = true
					return nil
				}
				if factoryURL == "" {
					factoryURL = configs.TalosExtensions.FactoryURL
				}
				return nil
			},
		},
		{
			Name: "System Extensions",
			Run: func() error {
				if cancelled {
					return nil
				}
				selected = selectTalosExtensions(
					configs.TalosExtensions.Extensions,
					configs.TalosExtensions.RecommendedExtensions,
					configs.TalosExtensions.DefaultExtensions,
				)
				if len(selected) == 0 {
					fmt.Println("  At least one extension is required")
				}
				return nil
			},
		},
	}); err != nil {
		return err
	}
	if cancelled || name == "" || len(selected) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Print("  Uploading schematic to Image Factory... ")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := createTalosSchematicID(ctx, factoryURL, selected)
	if err != nil {
		fmt.Printf("\033[31m✗\033[0m\n")
		return err
	}
	fmt.Printf("\033[32m✓\033[0m\n")
	fmt.Printf("  Generated schematic ID: \033[36m%s\033[0m\n", id)

	now := time.Now().UTC().Format(time.RFC3339)
	entry := talosSchematicEntry{
		Name:       name,
		ID:         id,
		Extensions: selected,
		CreatedAt:  now,
	}

	updated := false
	for i := range cfg.Talos.Schematics {
		if cfg.Talos.Schematics[i].Name == name {
			cfg.Talos.Schematics[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		cfg.Talos.Schematics = append(cfg.Talos.Schematics, entry)
	}
	sort.Slice(cfg.Talos.Schematics, func(i, j int) bool {
		return cfg.Talos.Schematics[i].Name < cfg.Talos.Schematics[j].Name
	})
	cfg.Talos.FactoryURL = factoryURL

	if cfg.Talos.Default == "" || readYesNo(fmt.Sprintf("Set '%s' as default schematic?", name), cfg.Talos.Default == name) {
		cfg.Talos.Default = name
	}

	if err := saveTalosSchematics(talosSchematicsConfigFile, cfg); err != nil {
		return err
	}
	_ = session.Finalize()
	fmt.Printf("\n\033[32m✓ Saved and encrypted: %s\033[0m\n", filepath.Base(talosSchematicsConfigFile))
	return nil
}

func findSchematicName(entries []talosSchematicEntry, input string) (string, bool) {
	needle := strings.ToLower(strings.TrimSpace(input))
	if needle == "" {
		return "", false
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if strings.ToLower(name) == needle {
			return name, true
		}
	}
	return "", false
}

func selectTalosSchematicID(current string) string {
	for {
		cfg, err := loadTalosSchematics(talosSchematicsConfigFile)
		if err != nil || len(cfg.Talos.Schematics) == 0 {
			return strings.TrimSpace(readLine("Talos schematic ID", current))
		}

		labelToID := make(map[string]string, len(cfg.Talos.Schematics))
		options := make([]string, 0, len(cfg.Talos.Schematics)+2)
		defaultOption := ""
		for _, s := range cfg.Talos.Schematics {
			label := s.Name + " — " + shortID(s.ID)
			if s.Name == cfg.Talos.Default {
				label += " (default)"
				defaultOption = label
			}
			if strings.TrimSpace(current) != "" && s.ID == strings.TrimSpace(current) {
				defaultOption = label
			}
			options = append(options, label)
			labelToID[label] = s.ID
		}
		sort.Strings(options)
		options = append(options, "Custom schematic ID...")
		options = append(options, "Manage schematics...")

		if defaultOption == "" {
			defaultOption = options[0]
		}
		choice := sel.Select(options, defaultOption, "Talos schematic:")
		switch choice {
		case "Custom schematic ID...":
			return strings.TrimSpace(readLine("Talos schematic ID", current))
		case "Manage schematics...":
			_ = runTalosConfigWizard()
			fmt.Println()
			continue
		default:
			return labelToID[choice]
		}
	}
}

func selectTalosExtensions(catalog, recommended, defaults []string) []string {
	fmt.Println("[2/2] System Extensions")
	fmt.Println("  Use Space to toggle, Enter to confirm.")

	ordered := make([]string, 0, len(catalog))
	seen := map[string]struct{}{}
	for _, ext := range catalog {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		ordered = append(ordered, ext)
	}
	sort.Strings(ordered)

	recommendedList := make([]string, 0, len(recommended))
	for _, ext := range ordered {
		if slices.Contains(recommended, ext) {
			recommendedList = append(recommendedList, ext)
		}
	}
	if len(recommendedList) == 0 {
		recommendedList = append(recommendedList, ordered...)
	}

	defaultSelected := make([]string, 0, len(defaults))
	for _, ext := range recommendedList {
		if slices.Contains(defaults, ext) {
			defaultSelected = append(defaultSelected, ext)
		}
	}
	if len(defaultSelected) == 0 && len(recommendedList) > 0 {
		defaultSelected = append(defaultSelected, recommendedList[0])
	}

	selected := sel.MultiSelect(recommendedList, defaultSelected, "Select recommended extensions:")
	if sel.WasInterrupted() {
		fmt.Println("  Cancelled.")
		return nil
	}

	if len(ordered) > len(recommendedList) {
		action := sel.Select(
			[]string{"Continue", "Load full extension list"},
			"Continue",
			"Next step:",
		)
		if action == "Load full extension list" {
			fullDefault := uniqueSorted(selected)
			fullSelected := sel.MultiSelect(ordered, fullDefault, "Select extensions (full list):")
			if sel.WasInterrupted() {
				// ESC/Ctrl+C here should just close full list and keep recommended selection.
				fmt.Println("  Full list closed.")
				fullSelected = fullDefault
			}
			selected = fullSelected
		}
	}

	for readYesNo("Add custom extension?", false) {
		ext := strings.TrimSpace(readLine("Custom extension (e.g. siderolabs/....)", ""))
		if ext == "" {
			fmt.Println("  Empty value skipped.")
			continue
		}
		selected = append(selected, ext)
	}

	selected = uniqueSorted(selected)
	fmt.Println()
	fmt.Println("  Selected extensions:")
	for _, ext := range selected {
		fmt.Printf("  - %s\n", ext)
	}
	return selected
}

func createTalosSchematicID(ctx context.Context, factoryURL string, extensions []string) (string, error) {
	schematic := map[string]any{"customization": map[string]any{}}
	if len(extensions) > 0 {
		schematic["customization"] = map[string]any{
			"systemExtensions": map[string]any{
				"officialExtensions": extensions,
			},
		}
	}
	payload, err := yaml.Marshal(schematic)
	if err != nil {
		return "", fmt.Errorf("marshal schematic yaml: %w", err)
	}

	url := strings.TrimRight(factoryURL, "/") + "/schematics"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/yaml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload schematic: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode image factory response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("image factory error: status %d, response: %v", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("image factory response missing schematic id")
	}
	return id, nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "..."
}
