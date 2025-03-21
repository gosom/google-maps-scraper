package postgres

import (
	"os"
	"path/filepath"
)

// GetMigrationPaths returns all valid migrations directories
func GetMigrationPaths() []string {
	// Try standard locations
	searchPaths := []string{
		"scripts/migrations",                   // Relative to working directory
		filepath.Join("scripts", "migrations"), // Alternative relative path
	}

	// Add absolute paths
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		searchPaths = append(searchPaths, filepath.Join(execDir, "scripts", "migrations"))
	}

	workingDir, err := os.Getwd()
	if err == nil {
		searchPaths = append(searchPaths, filepath.Join(workingDir, "scripts", "migrations"))
	}

	// Check each location
	var validPaths []string
	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			validPaths = append(validPaths, path)
		}
	}

	return validPaths
}