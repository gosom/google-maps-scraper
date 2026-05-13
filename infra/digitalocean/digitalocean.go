package digitalocean

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/digitalocean/godo"

	"github.com/gosom/google-maps-scraper/infra"
)

var _ infra.WorkerProvisioner = (*provisioner)(nil)

func init() {
	infra.RegisterWorkerProvisioner("digitalocean", func(token string) infra.WorkerProvisioner { //nolint:gocritic // unlambda: New returns *provisioner, not infra.WorkerProvisioner; wrapper required for interface conversion
		return New(token)
	})
}

type provisioner struct {
	client *godo.Client
}

// New creates a new DigitalOcean WorkerProvisioner.
func New(token string) infra.WorkerProvisioner {
	return &provisioner{
		client: godo.NewFromToken(token),
	}
}

// EnsureSSHKey ensures the given public key exists on DigitalOcean and returns its ID.
// If a key with matching content already exists, its ID is returned.
func (p *provisioner) EnsureSSHKey(ctx context.Context, pubKey string) (string, error) {
	pubKey = strings.TrimSpace(pubKey)

	// List all existing keys and compare by public key content.
	opt := &godo.ListOptions{PerPage: 200}

	for {
		keys, resp, err := p.client.Keys.List(ctx, opt)
		if err != nil {
			return "", fmt.Errorf("failed to list SSH keys: %w", err)
		}

		for _, k := range keys {
			if strings.TrimSpace(k.PublicKey) == pubKey {
				return strconv.Itoa(k.ID), nil
			}
		}

		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return "", fmt.Errorf("failed to get current page: %w", err)
		}

		opt.Page = page + 1
	}

	// Key not found — create it.
	key, _, err := p.client.Keys.Create(ctx, &godo.KeyCreateRequest{
		Name:      "gmapspro-worker",
		PublicKey: pubKey,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create SSH key: %w", err)
	}

	return strconv.Itoa(key.ID), nil
}

// ListRegions returns all available DigitalOcean regions.
func (p *provisioner) ListRegions(ctx context.Context) ([]infra.Region, error) {
	var regions []infra.Region

	opt := &godo.ListOptions{PerPage: 200}

	for {
		doRegions, resp, err := p.client.Regions.List(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list regions: %w", err)
		}

		for _, r := range doRegions {
			if !r.Available {
				continue
			}

			regions = append(regions, infra.Region{
				Slug: r.Slug,
				Name: r.Name,
			})
		}

		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return nil, fmt.Errorf("failed to get current page: %w", err)
		}

		opt.Page = page + 1
	}

	return regions, nil
}

// ListSizes returns all available DigitalOcean droplet sizes.
func (p *provisioner) ListSizes(ctx context.Context) ([]infra.Size, error) {
	var sizes []infra.Size

	opt := &godo.ListOptions{PerPage: 200}

	for {
		doSizes, resp, err := p.client.Sizes.List(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list sizes: %w", err)
		}

		for i := range doSizes {
			s := doSizes[i]
			if !s.Available {
				continue
			}

			sizes = append(sizes, infra.Size{
				Slug:         s.Slug,
				Description:  s.Description,
				PriceMonthly: s.PriceMonthly,
				VCPUs:        s.Vcpus,
				Memory:       s.Memory,
				Disk:         s.Disk,
			})
		}

		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return nil, fmt.Errorf("failed to get current page: %w", err)
		}

		opt.Page = page + 1
	}

	return sizes, nil
}

// CreateWorker creates a new DigitalOcean droplet configured as a worker.
func (p *provisioner) CreateWorker(ctx context.Context, req *infra.WorkerCreateRequest) (*infra.WorkerCreateResult, error) {
	// Ensure the SSH key exists and get its ID.
	keyIDStr, err := p.EnsureSSHKey(ctx, req.SSHPubKey)
	if err != nil {
		return nil, err
	}

	keyID, err := strconv.Atoi(keyIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH key ID: %w", err)
	}

	tags := append([]string{"gmapspro-worker"}, req.Tags...)

	createReq := &godo.DropletCreateRequest{
		Name:   req.Name,
		Region: req.Region,
		Size:   req.Size,
		Image: godo.DropletCreateImage{
			Slug: "ubuntu-24-04-x64",
		},
		SSHKeys: []godo.DropletCreateSSHKey{
			{ID: keyID},
		},
		UserData:   req.UserData,
		Monitoring: true,
		Tags:       tags,
	}

	droplet, _, err := p.client.Droplets.Create(ctx, createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create droplet: %w", err)
	}

	return &infra.WorkerCreateResult{
		ResourceID: strconv.Itoa(droplet.ID),
		Name:       droplet.Name,
		Region:     req.Region,
		Size:       req.Size,
		Status:     droplet.Status,
		IPAddress:  extractPublicIPv4(droplet),
	}, nil
}

// GetWorkerStatus returns the current status and public IP of a droplet.
func (p *provisioner) GetWorkerStatus(ctx context.Context, resourceID string) (*infra.WorkerStatus, error) {
	id, err := strconv.Atoi(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid resource ID: %w", err)
	}

	droplet, _, err := p.client.Droplets.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get droplet: %w", err)
	}

	return &infra.WorkerStatus{
		ResourceID: resourceID,
		Status:     droplet.Status,
		IPAddress:  extractPublicIPv4(droplet),
	}, nil
}

// extractPublicIPv4 returns the public IPv4 address of a droplet, or empty string if unavailable.
func extractPublicIPv4(d *godo.Droplet) string {
	ip, _ := d.PublicIPv4()
	return ip
}

// DeleteWorker destroys a DigitalOcean droplet.
// Returns nil if the droplet is already gone (idempotent).
func (p *provisioner) DeleteWorker(ctx context.Context, resourceID string) error {
	id, err := strconv.Atoi(resourceID)
	if err != nil {
		return fmt.Errorf("invalid resource ID: %w", err)
	}

	resp, err := p.client.Droplets.Delete(ctx, id)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return nil
		}

		return fmt.Errorf("failed to delete droplet: %w", err)
	}

	return nil
}
