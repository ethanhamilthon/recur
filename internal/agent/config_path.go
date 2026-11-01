package agent

import "path/filepath"

// PrimaryConfig returns the primary configuration file path for writeback.
func (a *Agent) PrimaryConfig() string {
	if a == nil {
		return ""
	}
	if a.primaryConfig != "" {
		return a.primaryConfig
	}
	if a.home != "" {
		return filepath.Join(a.home, "config.toml")
	}
	return ""
}
