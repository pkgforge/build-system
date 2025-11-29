package metadata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
// by querying the GitHub API directly
func FetchGHCRPackages() ([]GHCRPackage, error) {
	fmt.Println("Querying GitHub API for pkgforge GHCR packages...")

	var allPackages []GHCRPackage
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/pkgforge/packages?package_type=container&per_page=%d&page=%d", perPage, page)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Add GitHub token if available for higher rate limits
		if token := getGitHubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch GHCR packages: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch GHCR packages: status %d", resp.StatusCode)
		}

		var packages []GHCRPackage
		if err := json.NewDecoder(resp.Body).Decode(&packages); err != nil {
			return nil, fmt.Errorf("failed to parse GHCR packages: %w", err)
		}

		if len(packages) == 0 {
			break
		}

		allPackages = append(allPackages, packages...)

		fmt.Printf("  Fetched page %d (%d packages so far)\n", page, len(allPackages))

		// If we got less than perPage, we're on the last page
		if len(packages) < perPage {
			break
		}

		page++
	}

	return allPackages, nil
}

// getGitHubToken retrieves GitHub token from environment
func getGitHubToken() string {
	// Try common environment variable names
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}
	return ""
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
