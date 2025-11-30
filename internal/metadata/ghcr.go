package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
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
	maxRetries := 3

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/pkgforge/packages?package_type=container&per_page=%d&page=%d", perPage, page)

		var packages []GHCRPackage
		var lastErr error

		// Retry logic for transient errors
		for attempt := 1; attempt <= maxRetries; attempt++ {
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}

			// GitHub token is required for org packages API
			token := getGitHubToken()
			if token == "" {
				return nil, fmt.Errorf("GITHUB_TOKEN environment variable is required to query org packages")
			}
			req.Header.Set("Authorization", "token "+token)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("failed to fetch GHCR packages (attempt %d/%d): %w", attempt, maxRetries, err)
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Read and close body immediately to avoid resource leaks
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()

			if err != nil {
				lastErr = fmt.Errorf("failed to read response body (attempt %d/%d): %w", attempt, maxRetries, err)
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Handle non-200 status codes with retry for server errors
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("failed to fetch GHCR packages: status %d", resp.StatusCode)
				// Retry on 5xx errors (server-side issues)
				if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxRetries {
					fmt.Printf("  Server error (status %d) on page %d, retrying in %d seconds... (attempt %d/%d)\n",
						resp.StatusCode, page, attempt, attempt, maxRetries)
					time.Sleep(time.Duration(attempt*2) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Parse JSON response
			if err := json.Unmarshal(body, &packages); err != nil {
				return nil, fmt.Errorf("failed to parse GHCR packages: %w", err)
			}

			// Success - break out of retry loop
			break
		}

		// Check if we got packages
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

		// Add a small delay between requests to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return allPackages, nil
}

// getGitHubToken retrieves GitHub token from environment
func getGitHubToken() string {
	// Try common environment variable names
	if token := os.Getenv("GHCR_TOKEN"); token != "" {
		return token
	}
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
