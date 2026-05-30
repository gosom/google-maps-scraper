package infra

import (
	"context"
	"errors"
)

var ErrConnectionFailed = errors.New("connection failed")

type DatabaseInfo struct {
	ConnectionURL string
}

type Provisioner interface {
	// CheckConnectivity checks if the provision can access the provider
	CheckConnectivity(ctx context.Context) error
	// ExecuteCommand executes a command on the provider
	ExecuteCommand(ctx context.Context, command string) (string, error)
	// CreateDatabase provisions a database in the provier
	CreateDatabase(ctx context.Context) (*DatabaseInfo, error)
	// Deploy deploys the provided config on the provider
	Deploy(ctx context.Context, cfg *DeployConfig) error
}

// DeployConfig is a struct that holds all the required inforation a provider
// neds to deploy an image
type DeployConfig struct {
	Registry      *RegistryConfig
	DatabaseURL   string
	EncryptionKey string
	HashSalt      string
}

type DockerImageBuilder interface {
	BuildImage(ctx context.Context, dockerfilePath, imageName string) error
}

// RegistryConfig is a struct that holds the required data
// to connect to a docker registry
type RegistryConfig struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Token    string `json:"token"`
	Image    string `json:"image"`
}

// VPSConfig is a struct that holds the required data to connect
// to a VPS server
type VPSConfig struct {
	Host    string `json:"host"`
	Port    string `json:"port"`
	User    string `json:"user"`
	KeyPath string `json:"key_path"`
	Domain  string `json:"domain"`
}

// SSHKey just represents an ssh key pair
type SSHKey struct {
	Key string `json:"key"`
	Pub string `json:"pub"`
}

// DOConfig holds DigitalOcean App Platform provisioning configuration.
type DOConfig struct {
	Token    string `json:"token"`
	Region   string `json:"region"`              // App Platform region (e.g., "nyc")
	DBSize   string `json:"db_size,omitempty"`   // e.g., "db-s-1vcpu-1gb"
	DBRegion string `json:"db_region,omitempty"` // e.g., "nyc3"
	AppID    string `json:"app_id,omitempty"`    // set after deploy
	DBID     string `json:"db_id,omitempty"`     // set after DB creation
}

// HetznerConfig holds Hetzner Cloud provisioning configuration.
type HetznerConfig struct {
	Token    string `json:"token"`
	ServerID int64  `json:"server_id,omitempty"` // set after server creation
}

// PlanetScaleConfig holds PlanetScale database configuration.
type PlanetScaleConfig struct {
	Token string `json:"token"`
	Org   string `json:"org"`
}
