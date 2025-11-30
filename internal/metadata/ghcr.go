package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GHCRPackage represents a package from GHCR
type GHCRPackage struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	UpdatedAt  string `json:"updated_at"`
	URL        string `json:"url"`
}

// SBuildEntry represents an entry in SBUILD_LIST.json
type SBuildEntry struct {
	Disabled    bool   `json:"_disabled"`
	Rebuild     bool   `json:"rebuild"`
	PkgFamily   string `json:"pkg_family"`
	Description string `json:"description"`
	GHCRPkg     string `json:"ghcr_pkg"`
	BuildScript string `json:"build_script"`
}

const (
	// Release asset URLs (preferred)
	BincacheReleaseURL = "https://github.com/pkgforge/build-system/releases/latest/download/bincache-SBUILD_LIST.json"
	PkgcacheReleaseURL = "https://github.com/pkgforge/build-system/releases/latest/download/pkgcache-SBUILD_LIST.json"

	// Fallback URLs (legacy repos)
	BincacheFallbackURL = "https://raw.githubusercontent.com/pkgforge/bincache/refs/heads/main/SBUILD_LIST.json"
	PkgcacheFallbackURL = "https://raw.githubusercontent.com/pkgforge/pkgcache/refs/heads/main/SBUILD_LIST.json"

	// Minisign public key path
	MinisignPubKeyPath = "keys/minisign.pub"
)

// fetchWithFallback tries primary URL first, falls back to secondary URL
func fetchWithFallback(primaryURL, fallbackURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	// Try primary URL (release asset)
	resp, err := client.Get(primaryURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			fmt.Printf("  ✓ Fetched from release asset\n")
			return body, nil
		}
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Fallback to legacy repo URL
	fmt.Printf("  Release asset not found, using fallback URL...\n")
	resp, err = client.Get(fallbackURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from both URLs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch SBUILD_LIST: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SBUILD_LIST: %w", err)
	}

	fmt.Printf("  ✓ Fetched from fallback URL\n")
	return body, nil
}

// verifyMinisign verifies a file's minisign signature
func verifyMinisign(dataPath, sigPath, pubKeyPath string) error {
	// Check if minisign is available
	if _, err := exec.LookPath("minisign"); err != nil {
		fmt.Printf("  ⚠ minisign not found, skipping signature verification\n")
		return nil // Don't fail if minisign is not available
	}

	// Check if public key exists
	if _, err := os.Stat(pubKeyPath); os.IsNotExist(err) {
		fmt.Printf("  ⚠ Public key not found at %s, skipping verification\n", pubKeyPath)
		return nil // Don't fail if key doesn't exist yet
	}

	// Check if signature file exists
	if _, err := os.Stat(sigPath); os.IsNotExist(err) {
		fmt.Printf("  ⚠ Signature file not found, skipping verification\n")
		return nil // Don't fail if signature doesn't exist (fallback URLs won't have sigs)
	}

	// Verify signature
	cmd := exec.Command("minisign", "-V", "-p", pubKeyPath, "-m", dataPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("signature verification failed: %w\nOutput: %s", err, string(output))
	}

	fmt.Printf("  ✓ Signature verified\n")
	return nil
}

// GHCRPackage represents a package from GHCR_PKGS.json
type GHCRPackageInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"` // e.g., "bincache/a-utils/official/cal"
}

// GenerateGHCRPackageList fetches all packages from GitHub API and generates GHCR_PKGS.json
func GenerateGHCRPackageList(outputPath string) error {
	const apiURL = "https://api.github.com/orgs/pkgforge/packages?package_type=container&per_page=100"

	token := os.Getenv("GHCR_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	var allPackages []GHCRPackageInfo
	page := 1

	fmt.Println("Fetching packages from GitHub API...")

	for {
		url := fmt.Sprintf("%s&page=%d", apiURL, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to fetch page %d: %w", page, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d for page %d", resp.StatusCode, page)
		}

		var packages []GHCRPackage
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		if err := json.Unmarshal(body, &packages); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}

		if len(packages) == 0 {
			break
		}

		// Convert to GHCRPackageInfo format
		for _, pkg := range packages {
			allPackages = append(allPackages, GHCRPackageInfo{
				ID:   pkg.ID,
				Name: pkg.Name,
			})
		}

		fmt.Printf("  Page %d: %d packages (total: %d)\n", page, len(packages), len(allPackages))
		page++
	}

	// Write JSON
	data, err := json.MarshalIndent(allPackages, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Compress with zstd
	zstdPath := outputPath + ".zstd"
	cmd := exec.Command("zstd", "--ultra", "-22", "--force", outputPath, "-o", zstdPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}

	fmt.Printf("  ✓ Generated %s (%d packages)\n", zstdPath, len(allPackages))
	return nil
}

// FetchGHCRPackageList downloads and parses GHCR_PKGS.json.zstd from releases
func FetchGHCRPackageList() ([]string, error) {
	// Try to fetch from build-system releases first
	const releaseURL = "https://github.com/pkgforge/build-system/releases/latest/download/GHCR_PKGS.json.zstd"
	const fallbackURL = "https://raw.githubusercontent.com/pkgforge/metadata/refs/heads/main/GHCR_PKGS.json.zstd"

	client := &http.Client{Timeout: 120 * time.Second}

	fmt.Println("Downloading GHCR package list...")

	// Try release URL first
	resp, err := client.Get(releaseURL)
	var urlUsed string
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		// Fallback to metadata repo
		fmt.Println("  Release asset not found, using fallback...")
		resp, err = client.Get(fallbackURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch GHCR_PKGS.json.zstd: %w", err)
		}
		urlUsed = fallbackURL
	} else {
		urlUsed = releaseURL
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch GHCR_PKGS: status %d", resp.StatusCode)
	}
	fmt.Printf("  ✓ Fetched from %s\n", urlUsed)

	// Save compressed file temporarily
	tmpFile, err := os.CreateTemp("", "ghcr-pkgs-*.json.zstd")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if _, err := tmpFile.Write(body); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Decompress with zstd
	tmpJSON := tmpFile.Name() + ".json"
	defer os.Remove(tmpJSON)

	cmd := exec.Command("zstd", "-d", tmpFile.Name(), "-o", tmpJSON)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}

	// Read and parse JSON
	data, err := os.ReadFile(tmpJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON: %w", err)
	}

	// Try to parse as simple string array first (slimmed format from our releases)
	var names []string
	if err := json.Unmarshal(data, &names); err == nil {
		// Success - it's the slimmed format
		fmt.Printf("  ✓ Found %d GHCR packages (slimmed format)\n", len(names))
		return names, nil
	}

	// Fall back to full object format (from metadata repo)
	var pkgs []GHCRPackageInfo
	if err := json.Unmarshal(data, &pkgs); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Extract package names
	for _, pkg := range pkgs {
		names = append(names, pkg.Name)
	}

	fmt.Printf("  ✓ Found %d GHCR packages (full format)\n", len(names))
	return names, nil
}

// DownloadMetadata downloads metadata JSON from meta.pkgforge.dev
func DownloadMetadata(url, outputPath string) error {
	client := &http.Client{Timeout: 120 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch metadata: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	// Write to output file
	if err := os.WriteFile(outputPath, body, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	fmt.Printf("  ✓ Downloaded %d bytes\n", len(body))
	return nil
}

// FetchPackagesFromSBuildList fetches package names from SBUILD_LIST.json
// with release asset fallback and optional minisign verification
func FetchPackagesFromSBuildList(primaryURL, fallbackURL string) ([]string, error) {
	// Fetch data with fallback
	body, err := fetchWithFallback(primaryURL, fallbackURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SBUILD_LIST: %w", err)
	}

	// Save to temp file for minisign verification
	tmpFile, err := os.CreateTemp("", "sbuild-*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.Write(body); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Try to fetch and verify signature (only for release assets)
	sigURL := primaryURL + ".sig"
	sigResp, err := http.Get(sigURL)
	if err == nil && sigResp.StatusCode == http.StatusOK {
		sigBody, err := io.ReadAll(sigResp.Body)
		sigResp.Body.Close()
		if err == nil {
			sigFile := tmpFile.Name() + ".sig"
			if err := os.WriteFile(sigFile, sigBody, 0644); err == nil {
				defer os.Remove(sigFile)

				// Verify signature
				if err := verifyMinisign(tmpFile.Name(), sigFile, MinisignPubKeyPath); err != nil {
					return nil, fmt.Errorf("signature verification failed: %w", err)
				}
			}
		}
	} else if sigResp != nil {
		sigResp.Body.Close()
	}

	// Parse JSON
	var entries []SBuildEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse SBUILD_LIST: %w", err)
	}

	var packages []string
	for _, entry := range entries {
		// Skip disabled packages
		if entry.Disabled {
			continue
		}

		// Extract package name from ghcr_pkg
		// Format: "ghcr.io/pkgforge/bincache/40four/official"
		// We want: "bincache/40four/official"
		pkgName := strings.TrimPrefix(entry.GHCRPkg, "ghcr.io/pkgforge/")
		if pkgName != "" {
			packages = append(packages, pkgName)
		}
	}

	return packages, nil
}

