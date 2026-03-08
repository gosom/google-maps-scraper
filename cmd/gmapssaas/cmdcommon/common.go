package cmdcommon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/infra/vps"
)

var ErrDockerLoginFailed = errors.New("docker login failed")

// BuildAndPushImage builds and pushes a Docker image to the registry.
// Returns ErrDockerLoginFailed if login fails (credentials may be invalid).
func BuildAndPushImage(registry *infra.RegistryConfig) error {
	var fullImage string
	if strings.Contains(registry.Image, "/") {
		fullImage = registry.Image
	} else {
		fullImage = registry.URL + "/" + registry.Image
	}

	fmt.Printf("Logging into registry: %s\n", registry.URL)

	loginCmd := exec.Command("docker", "login", registry.URL, "-u", registry.Username, "--password-stdin") //nolint:gosec // Arguments are from trusted config
	loginCmd.Stdin = strings.NewReader(registry.Token)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr

	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("%w: %v", ErrDockerLoginFailed, err)
	}

	fmt.Printf("Building Docker image: %s\n", fullImage)

	buildCmd := exec.Command("docker", "build", "-t", fullImage, "-f", "Dockerfile.saas", ".")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	fmt.Printf("Pushing Docker image: %s\n", fullImage)

	pushCmd := exec.Command("docker", "push", fullImage)
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr

	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("docker push failed: %w", err)
	}

	fmt.Println("Image pushed successfully!")

	return nil
}

func NewVPSProvisioner(vpsCfg *infra.VPSConfig) (infra.Provisioner, error) {
	key, err := os.ReadFile(vpsCfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	return vps.New(
		vps.WithHost(vpsCfg.Host),
		vps.WithPort(vpsCfg.Port),
		vps.WithUser(vpsCfg.User),
		vps.WithPrivateKey(key),
	)
}

func GetAppURL(vpsCfg *infra.VPSConfig) string {
	if vpsCfg.Domain != "" {
		return "https://" + vpsCfg.Domain
	}

	return "https://" + vpsCfg.Host
}

// NewVPSProvisionerWithKey creates a VPS provisioner from key content (not a file path).
func NewVPSProvisionerWithKey(vpsCfg *infra.VPSConfig, privateKey []byte) (infra.Provisioner, error) {
	return vps.New(
		vps.WithHost(vpsCfg.Host),
		vps.WithPort(vpsCfg.Port),
		vps.WithUser(vpsCfg.User),
		vps.WithPrivateKey(privateKey),
	)
}
