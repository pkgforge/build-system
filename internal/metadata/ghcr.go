package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GHCRPackage represents a package from GHCR
type GHCRPackage struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	UpdatedAt  string `json:"updated_at"`
	URL        string `json:"url"`
}

// FetchGHCRPackages fetches all public packages from GitHub Container Registry
func FetchGHCRPackages() ([]GHCRPackage, error) {
	// Fetch compressed package list from metadata repo
	url := "https://raw.githubusercontent.com/pkgforge/metadata/refs/heads/main/GHCR_PKGS.json.zstd"

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GHCR packages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch GHCR packages: status %d", resp.StatusCode)
	}

	// Save to temp file for decompression
	tmpFile := "/tmp/ghcr_pkgs.json.zstd"
	out, err := createFile(tmpFile)
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to save GHCR packages: %w", err)
	}

	// Decompress using zstd command
	decompressed := "/tmp/ghcr_pkgs.json"
	if err := runCommand("zstd", "--decompress", "--force", tmpFile, "-o", decompressed); err != nil {
		return nil, fmt.Errorf("failed to decompress GHCR packages: %w", err)
	}

	// Read and parse JSON
	data, err := readFile(decompressed)
	if err != nil {
		return nil, err
	}

	var packages []GHCRPackage
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("failed to parse GHCR packages: %w", err)
	}

	return packages, nil
}

// FilterBincachePackages filters GHCR packages for bincache
func FilterBincachePackages(packages []GHCRPackage) []string {
	var result []string

	for _, pkg := range packages {
		// Only public packages
		if pkg.Visibility != "public" {
			continue
		}

		// Filter for bincache packages (not srcbuild)
		if strings.Contains(pkg.Name, "-srcbuild") {
			continue
		}

		if strings.Contains(pkg.Name, "bincache") {
			result = append(result, pkg.Name)
		}
	}

	return result
}

// FilterPkgcachePackages filters GHCR packages for pkgcache
func FilterPkgcachePackages(packages []GHCRPackage) []string {
	var result []string

	for _, pkg := range packages {
		// Only public packages
		if pkg.Visibility != "public" {
			continue
		}

		// Filter for pkgcache packages
		if strings.Contains(pkg.Name, "pkgcache") {
			result = append(result, pkg.Name)
		}
	}

	return result
}
