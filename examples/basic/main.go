// Package main demonstrates basic usage of the vmware-vm-bootstrap library.
//
// This example shows how to:
// - Configure a VM bootstrap
// - Connect to vCenter
// - Create a VM with Ubuntu autoinstall
// - Verify SSH access
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
)

func main() {
	// Get vCenter credentials from environment
	vcHost := getEnv("VCENTER_HOST", "vcenter.example.com")
	vcUser := getEnv("VCENTER_USERNAME", "administrator@vsphere.local")
	vcPass := getEnv("VCENTER_PASSWORD", "")

	if vcPass == "" {
		log.Fatal("VCENTER_PASSWORD environment variable is required")
	}

	// Configure VM bootstrap
	cfg := &bootstrap.VMConfig{
		// vCenter connection
		VCenterHost:     vcHost,
		VCenterUsername: vcUser,
		VCenterPassword: vcPass,
		VCenterPort:     443,
		VCenterInsecure: false, // Set to true for self-signed certs

		// VM specifications
		Name:       "example-vm-01",
		CPUs:       4,
		MemoryMB:   8192,
		DiskSizeGB: 40,

		// Optional: Data disk
		// DataDiskSizeGB: intPtr(500),

		// Network configuration
		NetworkName: "LAN_Management",
		IPAddress:   "192.168.1.100",
		Netmask:     "255.255.255.0",
		Gateway:     "192.168.1.1",
		DNS:         []string{"8.8.8.8", "8.8.4.4"},

		// vCenter placement
		Datacenter:   "DC1",
		Folder:       "Production",
		ResourcePool: "WebServers",
		Datastore:    "SSD-Storage-01",

		// OS and user configuration
		Profile: "ubuntu",
		Profiles: bootstrap.VMProfiles{
			Ubuntu: bootstrap.UbuntuProfile{Version: "24.04"},
		},
		Username: "sysadmin",
		SSHPublicKeys: []string{
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample... user@host",
		},

		// Optional: Advanced settings
		Timezone: "UTC",
		Locale:   "en_US.UTF-8",
		Firmware: "bios", // or "efi"
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	log.Printf("Starting VM bootstrap: %s", cfg.Name)

	// Bootstrap the VM
	vm, err := bootstrap.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("Bootstrap failed: %v", err)
	}

	log.Printf("VM %s created successfully!", vm.Name)
	log.Printf("IP Address: %s", vm.IPAddress)
	log.Printf("SSH Ready: %v", vm.SSHReady)
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
