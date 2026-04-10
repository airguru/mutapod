package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const filename = "mutapod.yaml"

// Load finds mutapod.yaml by walking up from dir and parses it.
// dir is typically the current working directory.
func Load(dir string) (*Config, error) {
	path, err := find(dir)
	if err != nil {
		return nil, err
	}
	return loadFile(path)
}

// LoadFile parses a specific mutapod.yaml file.
func LoadFile(path string) (*Config, error) {
	return loadFile(path)
}

func find(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("config: resolve dir: %w", err)
	}
	for {
		candidate := filepath.Join(dir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("config: %s not found in %s or any parent directory", filename, start)
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.Dir = filepath.Dir(path)
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Idle.Enabled == nil {
		enabled := true
		cfg.Idle.Enabled = &enabled
	}
	if cfg.Sync.LocalPath == "" {
		cfg.Sync.LocalPath = "."
	}
	if cfg.Sync.Mode == "" {
		cfg.Sync.Mode = "two-way-resolved"
	}
	if cfg.Idle.TimeoutMinutes == 0 {
		cfg.Idle.TimeoutMinutes = 30
	}
	if cfg.Idle.CheckIntervalSeconds == 0 {
		cfg.Idle.CheckIntervalSeconds = 60
	}
	// Apply GCP defaults
	if cfg.Provider.Type == "gcp" {
		gcp := &cfg.Provider.GCP
		if gcp.Zone == "" {
			gcp.Zone = "us-central1-a"
		}
		if gcp.MachineType == "" {
			gcp.MachineType = "e2-standard-4"
		}
		if gcp.DiskSizeGB == 0 {
			gcp.DiskSizeGB = 30
		}
		if gcp.DiskType == "" {
			gcp.DiskType = "pd-balanced"
		}
		if gcp.ImageFamily == "" {
			gcp.ImageFamily = "ubuntu-2204-lts"
		}
		if gcp.ImageProject == "" {
			gcp.ImageProject = "ubuntu-os-cloud"
		}
		if gcp.Labels == nil {
			gcp.Labels = map[string]string{"managed-by": "mutapod"}
		}
	}
}

func validate(cfg *Config) error {
	if cfg.Name == "" {
		return fmt.Errorf("config: 'name' is required")
	}
	if cfg.Provider.Type == "" {
		return fmt.Errorf("config: 'provider.type' is required")
	}
	switch cfg.Provider.Type {
	case "gcp":
		if cfg.Provider.GCP.Project == "" {
			return fmt.Errorf("config: 'provider.gcp.project' is required")
		}
	case "azure":
		return fmt.Errorf("config: provider type %q is not currently supported; use %q", cfg.Provider.Type, "gcp")
	default:
		return fmt.Errorf("config: unsupported provider type %q (supported: gcp)", cfg.Provider.Type)
	}
	return nil
}

// WorkspacePath returns the resolved remote workspace path.
// If RemotePath is set in config, that is used; otherwise /workspace/<name>.
func (c *Config) WorkspacePath() string {
	if c.Sync.RemotePath != "" {
		return c.Sync.RemotePath
	}
	return "/workspace/" + c.Name
}

// LocalSyncPath returns the absolute local path to sync.
func (c *Config) LocalSyncPath() (string, error) {
	p := c.Sync.LocalPath
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.Dir, p)
	}
	return filepath.Abs(p)
}
