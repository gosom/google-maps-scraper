package cmdprovision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gosom/google-maps-scraper/infra"
)

const (
	stateDir  = ".gmapssaas"
	stateFile = "provision-state.json"
)

type ProvisionState struct {
	Provider  string    `json:"provider"`
	StartedAt time.Time `json:"started_at"`

	VPS     *infra.VPSConfig     `json:"vps,omitempty"`
	DO      *infra.DOConfig      `json:"do,omitempty"`
	Hetzner *infra.HetznerConfig `json:"hetzner,omitempty"`

	Steps CompletedSteps `json:"steps"`

	DatabaseURL string `json:"database_url,omitempty"`

	Registry *infra.RegistryConfig `json:"registry,omitempty"`

	EncryptionKey string        `json:"encryption_key,omitempty"`
	HashSalt      string        `json:"hash_salt,omitempty"`
	SSHKey        *infra.SSHKey `json:"ssh_key,omitempty"`

	AdminUsername string `json:"admin_username,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`
}

type CompletedSteps struct {
	ConnectivityChecked bool `json:"connectivity_checked"`
	SetupCompleted      bool `json:"setup_completed"`
	DatabaseCreated     bool `json:"database_created"`
	ImagePushed         bool `json:"image_pushed"`
	Deployed            bool `json:"deployed"`
}

func stateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, stateDir, stateFile), nil
}

// EnsureStateDir creates the state directory if needed and verifies it is writable.
// Call this before any state operations to surface permission errors early
// (e.g. when running inside Docker with a mismatched UID).
func EnsureStateDir() error {
	path, err := stateFilePath()
	if err != nil {
		return fmt.Errorf("failed to determine state file path: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", dir, err)
	}

	// Verify write access by creating and removing a temp file.
	tmp := filepath.Join(dir, ".write-test")

	if err := os.WriteFile(tmp, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("state directory %s is not writable: %w", dir, err)
	}

	_ = os.Remove(tmp)

	return nil
}

func LoadState() (*ProvisionState, error) {
	path, err := stateFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var state ProvisionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

func SaveState(state *ProvisionState) error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func DeleteState() error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func StateExists() bool {
	path, err := stateFilePath()
	if err != nil {
		return false
	}

	_, err = os.Stat(path)

	return err == nil
}
