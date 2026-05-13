package hetzner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/gosom/google-maps-scraper/infra"
)

const sshKeyName = "gmapssaas-worker"

var _ infra.WorkerProvisioner = (*Provisioner)(nil)

func init() {
	infra.RegisterWorkerProvisioner("hetzner", func(token string) infra.WorkerProvisioner {
		return New(token)
	})
}

type Provisioner struct {
	client *hcloud.Client
}

// New creates a new Hetzner Cloud WorkerProvisioner.
func New(token string) *Provisioner {
	return &Provisioner{
		client: hcloud.NewClient(hcloud.WithToken(token)),
	}
}

func (p *Provisioner) EnsureSSHKey(ctx context.Context, pubKey string) (string, error) {
	pubKey = strings.TrimSpace(pubKey)

	keys, err := p.client.SSHKey.All(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list SSH keys: %w", err)
	}

	// Match by name first (most reliable), then by public key content
	for _, k := range keys {
		if k.Name == sshKeyName || strings.TrimSpace(k.PublicKey) == pubKey {
			return strconv.FormatInt(k.ID, 10), nil
		}
	}

	key, _, err := p.client.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{
		Name:      sshKeyName,
		PublicKey: pubKey,
	})
	if err != nil {
		// Key may have been created concurrently — look up by name as fallback
		if hcloud.IsError(err, hcloud.ErrorCodeUniquenessError) {
			existing, _, getErr := p.client.SSHKey.GetByName(ctx, sshKeyName)
			if getErr == nil && existing != nil {
				return strconv.FormatInt(existing.ID, 10), nil
			}
		}

		return "", fmt.Errorf("failed to create SSH key: %w", err)
	}

	return strconv.FormatInt(key.ID, 10), nil
}

func (p *Provisioner) ListRegions(ctx context.Context) ([]infra.Region, error) {
	locations, err := p.client.Location.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list locations: %w", err)
	}

	regions := make([]infra.Region, 0, len(locations))
	for _, l := range locations {
		regions = append(regions, infra.Region{
			Slug: l.Name,
			Name: fmt.Sprintf("%s (%s)", l.Description, l.City),
		})
	}

	return regions, nil
}

func (p *Provisioner) ListSizes(ctx context.Context) ([]infra.Size, error) {
	serverTypes, err := p.client.ServerType.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list server types: %w", err)
	}

	sizes := make([]infra.Size, 0, len(serverTypes))

	for _, t := range serverTypes {
		if t.Deprecation != nil {
			continue
		}

		// Skip ARM architecture — not supported
		if strings.EqualFold(string(t.Architecture), "arm") {
			continue
		}

		// Collect available (non-deprecated) regions from Locations.
		// Pricings lists all historical locations; Locations reflects current availability.
		regions := make([]string, 0, len(t.Locations))

		for _, loc := range t.Locations {
			if loc.Location != nil && !loc.IsDeprecated() {
				regions = append(regions, loc.Location.Name)
			}
		}

		// Use the pricing for the first available location.
		price := 0.0

		for i := range t.Pricings {
			pr := t.Pricings[i]
			if pr.Location != nil {
				price, _ = strconv.ParseFloat(pr.Monthly.Gross, 64)
				break
			}
		}

		sizes = append(sizes, infra.Size{
			Slug:         t.Name,
			Description:  t.Description,
			PriceMonthly: price,
			VCPUs:        t.Cores,
			Memory:       int(t.Memory * 1024), // GB to MB
			Disk:         t.Disk,
			Regions:      regions,
		})
	}

	return sizes, nil
}

func (p *Provisioner) CreateWorker(ctx context.Context, req *infra.WorkerCreateRequest) (*infra.WorkerCreateResult, error) {
	keyIDStr, err := p.EnsureSSHKey(ctx, req.SSHPubKey)
	if err != nil {
		return nil, err
	}

	keyID, _ := strconv.ParseInt(keyIDStr, 10, 64)

	result, _, err := p.client.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       req.Name,
		ServerType: &hcloud.ServerType{Name: req.Size},
		Location:   &hcloud.Location{Name: req.Region},
		Image:      &hcloud.Image{Name: "ubuntu-24.04"},
		SSHKeys:    []*hcloud.SSHKey{{ID: keyID}},
		UserData:   req.UserData,
		Labels:     map[string]string{"app": "gmapssaas-worker"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	ip := ""
	if result.Server.PublicNet.IPv4.IP != nil {
		ip = result.Server.PublicNet.IPv4.IP.String()
	}

	return &infra.WorkerCreateResult{
		ResourceID: strconv.FormatInt(result.Server.ID, 10),
		Name:       result.Server.Name,
		Region:     req.Region,
		Size:       req.Size,
		Status:     string(result.Server.Status),
		IPAddress:  ip,
	}, nil
}

func (p *Provisioner) GetWorkerStatus(ctx context.Context, resourceID string) (*infra.WorkerStatus, error) {
	id, _ := strconv.ParseInt(resourceID, 10, 64)

	server, _, err := p.client.Server.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	if server == nil {
		return nil, fmt.Errorf("server not found: %s", resourceID)
	}

	ip := ""
	if server.PublicNet.IPv4.IP != nil {
		ip = server.PublicNet.IPv4.IP.String()
	}

	return &infra.WorkerStatus{
		ResourceID: resourceID,
		Status:     string(server.Status),
		IPAddress:  ip,
	}, nil
}

// DeleteWorker destroys a Hetzner server.
// Returns nil if the server is already gone (idempotent).
func (p *Provisioner) DeleteWorker(ctx context.Context, resourceID string) error {
	id, err := strconv.ParseInt(resourceID, 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid hetzner server ID: %q", resourceID)
	}

	_, _, err = p.client.Server.DeleteWithResult(ctx, &hcloud.Server{ID: id})
	if err != nil {
		if hcloud.IsError(err, hcloud.ErrorCodeNotFound) {
			return nil
		}

		return fmt.Errorf("failed to delete server: %w", err)
	}

	return nil
}

// CreateServer creates a Hetzner Cloud server (for the provision command).
// It returns the server ID and public IP.
func (p *Provisioner) CreateServer(ctx context.Context, name, serverType, location, sshPubKey, userData string) (int64, string, error) { //nolint:gocritic // unnamedResult: return types are self-explanatory (serverID, ip, error)
	keyIDStr, err := p.EnsureSSHKey(ctx, sshPubKey)
	if err != nil {
		return 0, "", err
	}

	keyID, _ := strconv.ParseInt(keyIDStr, 10, 64)

	result, _, err := p.client.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       name,
		ServerType: &hcloud.ServerType{Name: serverType},
		Location:   &hcloud.Location{Name: location},
		Image:      &hcloud.Image{Name: "ubuntu-24.04"},
		SSHKeys:    []*hcloud.SSHKey{{ID: keyID}},
		UserData:   userData,
		Labels:     map[string]string{"app": "gmapssaas"},
	})
	if err != nil {
		return 0, "", fmt.Errorf("failed to create server (type=%s location=%s): %w", serverType, location, err)
	}

	ip := ""
	if result.Server.PublicNet.IPv4.IP != nil {
		ip = result.Server.PublicNet.IPv4.IP.String()
	}

	return result.Server.ID, ip, nil
}

// WaitForServer polls until the server is running and returns its IP.
func (p *Provisioner) WaitForServer(ctx context.Context, serverID int64) (string, error) {
	server, _, err := p.client.Server.GetByID(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("failed to get server: %w", err)
	}

	if server == nil {
		return "", fmt.Errorf("server %d not found", serverID)
	}

	ip := ""
	if server.PublicNet.IPv4.IP != nil {
		ip = server.PublicNet.IPv4.IP.String()
	}

	return ip, nil
}

// ListServerTypes returns available server types for the provision wizard.
func (p *Provisioner) ListServerTypes(ctx context.Context) ([]infra.Size, error) {
	return p.ListSizes(ctx)
}

// ListLocations returns available locations for the provision wizard.
func (p *Provisioner) ListLocations(ctx context.Context) ([]infra.Region, error) {
	return p.ListRegions(ctx)
}
