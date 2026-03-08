// Package vcenter provides a wrapper around the govmomi library for vCenter operations.
package vcenter

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/soap"
)

// Client wraps govmomi client and provides high-level vCenter operations.
type Client struct {
	conn   *govmomi.Client
	finder *find.Finder
	ctx    context.Context
}

// Config holds vCenter connection parameters.
type Config struct {
	Host     string // vCenter hostname or IP
	Username string // vCenter username
	Password string // vCenter password
	Port     int    // vCenter port (default: 443)
	Insecure bool   // Skip TLS verification (not recommended for production)
}

// NewClient creates a new vCenter client and connects to the vCenter server.
// Returns an error if connection fails.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = configs.Defaults.VCenter.Port
	}

	var vcURL *url.URL
	if strings.Contains(cfg.Host, "://") {
		parsed, err := url.Parse(cfg.Host)
		if err != nil {
			return nil, fmt.Errorf("invalid vCenter URL %q: %w", cfg.Host, err)
		}
		if parsed.Scheme == "" {
			parsed.Scheme = "https"
		}
		if parsed.Scheme != "https" {
			return nil, fmt.Errorf("unsupported vCenter URL scheme %q (https required)", parsed.Scheme)
		}
		if parsed.Path == "" {
			parsed.Path = "/sdk"
		}
		if parsed.Host == "" {
			return nil, fmt.Errorf("invalid vCenter URL (missing host): %q", cfg.Host)
		}
		if parsed.Port() == "" && cfg.Port != 0 {
			parsed.Host = fmt.Sprintf("%s:%d", parsed.Hostname(), cfg.Port)
		}
		vcURL = parsed
	} else {
		// Build vCenter URL from host + port
		vcURL = &url.URL{
			Scheme: "https",
			Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Path:   "/sdk",
		}
	}
	vcURL.User = url.UserPassword(cfg.Username, cfg.Password)

	// Connect to vCenter
	client, err := govmomi.NewClient(ctx, vcURL, cfg.Insecure)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to vCenter: %w", err)
	}

	// Create finder for object lookups
	finder := find.NewFinder(client.Client, true)

	return &Client{
		conn:   client,
		finder: finder,
		ctx:    ctx,
	}, nil
}

// Disconnect closes the vCenter connection.
func (c *Client) Disconnect() error {
	if c.conn != nil {
		return c.conn.Logout(c.ctx)
	}
	return nil
}

// FindDatacenter locates a datacenter by name.
func (c *Client) FindDatacenter(name string) (*object.Datacenter, error) {
	dc, err := c.finder.Datacenter(c.ctx, name)
	if err != nil {
		return nil, fmt.Errorf("datacenter %q not found: %w", name, err)
	}
	return dc, nil
}

// FindDatastore locates a datastore by name within a datacenter.
func (c *Client) FindDatastore(datacenter, name string) (*object.Datastore, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}

	c.finder.SetDatacenter(dc)
	ds, err := c.finder.Datastore(c.ctx, name)
	if err != nil {
		return nil, fmt.Errorf("datastore %q not found: %w", name, err)
	}
	return ds, nil
}

// FindNetwork locates a network by name within a datacenter.
func (c *Client) FindNetwork(datacenter, name string) (object.NetworkReference, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}

	c.finder.SetDatacenter(dc)
	net, err := c.finder.Network(c.ctx, name)
	if err != nil {
		return nil, fmt.Errorf("network %q not found: %w", name, err)
	}
	return net, nil
}

// FindFolder locates a VM folder by path within a datacenter.
// Path format: "/DC1/vm/Production/WebServers" or relative "Production/WebServers"
func (c *Client) FindFolder(datacenter, path string) (*object.Folder, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}

	c.finder.SetDatacenter(dc)
	folder, err := c.finder.Folder(c.ctx, path)
	if err != nil {
		return nil, fmt.Errorf("folder %q not found: %w", path, err)
	}
	return folder, nil
}

// FindResourcePool locates a resource pool by path within a datacenter.
// Path format: "/DC1/host/Cluster/Resources/Pool" or relative "Pool"
func (c *Client) FindResourcePool(datacenter, path string) (*object.ResourcePool, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}

	c.finder.SetDatacenter(dc)
	pool, err := c.finder.ResourcePool(c.ctx, path)
	if err != nil {
		return nil, fmt.Errorf("resource pool %q not found: %w", path, err)
	}
	return pool, nil
}

// FindVM locates a virtual machine by name within a datacenter.
// Returns nil if VM doesn't exist (no error).
func (c *Client) FindVM(datacenter, name string) (*object.VirtualMachine, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}

	c.finder.SetDatacenter(dc)
	vm, err := c.finder.VirtualMachine(c.ctx, name)
	if err != nil {
		// VM not found is not an error for idempotency checks
		if _, ok := err.(*find.NotFoundError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find VM %q: %w", name, err)
	}
	return vm, nil
}

// Client returns the underlying govmomi client for advanced operations.
func (c *Client) Client() *govmomi.Client {
	return c.conn
}

// SOAPClient returns the underlying SOAP client for low-level operations.
func (c *Client) SOAPClient() *soap.Client {
	return c.conn.Client.Client
}
