package testenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// VersionsFile is the name of the pin file at the repository root.
const VersionsFile = "harness-versions.json"

// Versions holds the pinned harness CLI versions.
type Versions struct {
	Claude string `json:"claude"`
	Codex  string `json:"codex"`
}

// LoadVersions reads the pins from harness-versions.json in root.
func LoadVersions(root string) (Versions, error) {
	path := filepath.Join(root, VersionsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Versions{}, fmt.Errorf("read harness versions: %w", err)
	}
	var v Versions
	if err := json.Unmarshal(data, &v); err != nil {
		return Versions{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if v.Claude == "" || v.Codex == "" {
		return Versions{}, errors.New("parse " + path + ": claude and codex pins are required")
	}
	return v, nil
}
