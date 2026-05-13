package infra

import (
	"context"
	"fmt"
)

// workerProvisionerFactories stores registered provider factory functions.
var workerProvisionerFactories = map[string]func(string) WorkerProvisioner{}

// RegisterWorkerProvisioner registers a factory function for a provider name.
// Called by provider packages in their init() functions.
func RegisterWorkerProvisioner(name string, factory func(string) WorkerProvisioner) {
	workerProvisionerFactories[name] = factory
}

// NewWorkerProvisioner creates a WorkerProvisioner for the given provider.
func NewWorkerProvisioner(provider, token string) (WorkerProvisioner, error) {
	factory, ok := workerProvisionerFactories[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported worker provider: %s", provider)
	}

	return factory(token), nil
}

// Region represents a cloud provider region.
type Region struct {
	Slug string
	Name string
}

// Size represents a cloud provider instance size.
type Size struct {
	Slug         string
	Description  string
	PriceMonthly float64
	VCPUs        int
	Memory       int      // MB
	Disk         int      // GB
	Regions      []string // region slugs where this size is available (empty = all)
}

// WorkerCreateRequest contains the parameters for creating a worker droplet.
type WorkerCreateRequest struct {
	Name      string
	Region    string
	Size      string
	SSHPubKey string // authorized_keys format
	UserData  string // cloud-init script (from cloudinit.Generate())
	Tags      []string
}

// WorkerCreateResult contains the result of creating a worker droplet.
type WorkerCreateResult struct {
	ResourceID string // provider-specific resource ID as string
	Name       string
	Region     string
	Size       string
	Status     string
	IPAddress  string // may be empty initially
}

// WorkerStatus contains the current status of a worker droplet.
type WorkerStatus struct {
	ResourceID string
	Status     string // e.g. "new", "active", "off", "archive"
	IPAddress  string
}

// WorkerProvisioner defines the interface for provisioning worker instances
// on a cloud provider. Separate from the Provisioner interface which handles
// main VPS management.
type WorkerProvisioner interface {
	// EnsureSSHKey ensures the given public key exists on the provider and returns its ID.
	EnsureSSHKey(ctx context.Context, pubKey string) (keyID string, err error)

	// ListRegions returns the available regions from the provider.
	ListRegions(ctx context.Context) ([]Region, error)

	// ListSizes returns the available instance sizes from the provider.
	ListSizes(ctx context.Context) ([]Size, error)

	// CreateWorker creates a new worker instance.
	CreateWorker(ctx context.Context, req *WorkerCreateRequest) (*WorkerCreateResult, error)

	// GetWorkerStatus returns the current status and IP of a worker instance.
	GetWorkerStatus(ctx context.Context, resourceID string) (*WorkerStatus, error)

	// DeleteWorker destroys a worker instance.
	DeleteWorker(ctx context.Context, resourceID string) error
}
