package metadata

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PackageMetadata represents metadata for a single package
type PackageMetadata struct {
	Name        string   `json:"pkg"`
	PkgID       string   `json:"pkg_id"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Size        string   `json:"size"`
	Bsum        string   `json:"bsum"`
	Shasum      string   `json:"shasum"`
	BuildDate   string   `json:"build_date"`
	BuildID     string   `json:"build_id"`
	BuildScript string   `json:"build_script"`
	Category    []string `json:"category"`    // Array of categories
	Checksum    string   `json:"checksum"`
	DownloadURL string   `json:"download_url"`
	GHCRPkg     string   `json:"ghcr_pkg"`
	Homepage    []string `json:"homepage"`    // Array of URLs
	Icon        string   `json:"icon"`
	License     []string `json:"license"`     // Array of licenses
	Maintainer  []string `json:"maintainer"`  // Array of maintainers
	Note        []string `json:"note"`        // Array of notes
	ProvidesPkg []string `json:"provides_pkg"`
	Provides    []string `json:"provides"`    // Alternative field name
	Repology    string   `json:"repology"`
	SrcURL      []string `json:"src_url"`     // Array of URLs
	Tag         []string `json:"tag"`         // Array of tags
	WebURL      string   `json:"web_url"`
}

// FetchConfig holds configuration for metadata fetching
type FetchConfig struct {
	OrasPath string
	Arch     string
	WorkDir  string
}

// ensureGHCRLogin ensures oras is logged in to GHCR (call once before fetching)
func ensureGHCRLogin(orasPath string) error {
	// Check if GHCR_TOKEN is set
	token := os.Getenv("GHCR_TOKEN")
	if token == "" {
		// No token, try without authentication
		return nil
	}

	// Login to GHCR using token
	cmd := exec.Command(orasPath, "login", "ghcr.io", "-u", "token", "--password-stdin")
	cmd.Stdin = strings.NewReader(token)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("oras login failed: %w (output: %s)", err, string(output))
	}

	fmt.Println("  ✓ Authenticated with GHCR")
	return nil
}

var pullErrorCount = 0

// QueryPackageMetadata fetches metadata from GHCR package manifest annotations
func QueryPackageMetadata(config FetchConfig, ghcrPkg string) (*PackageMetadata, error) {
	// Construct GHCR package reference (without tag)
	var pkgRef string
	if strings.HasPrefix(ghcrPkg, "ghcr.io/") {
		pkgRef = ghcrPkg
	} else {
		pkgRef = fmt.Sprintf("ghcr.io/pkgforge/%s", ghcrPkg)
	}

	// Step 1: Get latest tag for this architecture
	cmd := exec.Command(config.OrasPath, "repo", "tags", pkgRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ Failed to list tags for %s: %v\n", pkgRef, err)
		}
		return nil, nil
	}

	// Find the latest tag matching the architecture
	// Format: HEAD-hash-dateThms-x86_64-Linux or version-x86_64-Linux
	var latestTag string
	archPattern := strings.ToLower(config.Arch) // x86_64-Linux -> x86_64-linux
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		tag := strings.TrimSpace(scanner.Text())
		// Skip srcbuild tags and match architecture
		if !strings.Contains(tag, "srcbuild") && strings.Contains(strings.ToLower(tag), archPattern) {
			latestTag = tag
		}
	}

	if latestTag == "" {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ No tag found for %s with arch %s\n", pkgRef, config.Arch)
		}
		return nil, nil
	}

	// Step 2: Fetch manifest for the tag
	imageRef := fmt.Sprintf("%s:%s", pkgRef, latestTag)
	cmd = exec.Command(config.OrasPath, "manifest", "fetch", imageRef)
	output, err = cmd.CombinedOutput()
	if err != nil {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ Failed to fetch manifest for %s: %v\n", imageRef, err)
		}
		return nil, nil
	}

	// Step 3: Parse manifest JSON and extract metadata from annotations
	var manifest struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(output, &manifest); err != nil {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ Failed to parse manifest for %s: %v\n", imageRef, err)
		}
		return nil, nil
	}

	// Extract metadata from annotation
	metaJSON, ok := manifest.Annotations["dev.pkgforge.soar.json"]
	if !ok {
		// No metadata annotation, skip
		return nil, nil
	}

	// Parse metadata JSON
	var meta PackageMetadata
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ Failed to parse metadata JSON for %s: %v\n", imageRef, err)
		}
		return nil, nil
	}

	return &meta, nil
}

// constructBasicMetadata creates basic metadata when .METADATA file is not available
func constructBasicMetadata(pkgName, arch string) *PackageMetadata {
	// Extract package name from path
	// Example: "bincache/40four/official" -> "40four"
	parts := strings.Split(strings.TrimPrefix(pkgName, "ghcr.io/pkgforge/"), "/")

	var name string
	if len(parts) >= 2 {
		name = parts[len(parts)-2] // Get package name (second to last part)
	} else if len(parts) > 0 {
		name = parts[0]
	} else {
		name = pkgName
	}

	return &PackageMetadata{
		Name:    name,
		GHCRPkg: pkgName,
	}
}

// GenerateMetadataForPackages processes a list of packages and generates metadata
func GenerateMetadataForPackages(config FetchConfig, packages []string, outputPath string, parallel int) error {
	fmt.Printf("Processing %d packages with %d parallel workers...\n", len(packages), parallel)

	// Login to GHCR once before processing
	if err := ensureGHCRLogin(config.OrasPath); err != nil {
		return fmt.Errorf("failed to authenticate with GHCR: %w", err)
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	defer writer.Flush()

	// Write JSON array start
	writer.WriteString("[\n")

	count := 0
	errorCount := 0
	maxErrorsToShow := 5
	for i, pkg := range packages {
		if i%100 == 0 {
			fmt.Printf("Progress: %d/%d packages (successful: %d, errors: %d)...\n", i, len(packages), count, errorCount)
		}

		meta, err := QueryPackageMetadata(config, pkg)
		if err != nil {
			errorCount++
			if errorCount <= maxErrorsToShow {
				fmt.Printf("Warning: failed to query %s: %v\n", pkg, err)
			}
			continue
		}

		if meta == nil {
			// Package has no metadata, skip
			errorCount++
			continue
		}

		// Write JSON object
		if count > 0 {
			writer.WriteString(",\n")
		}

		data, err := json.Marshal(meta)
		if err != nil {
			fmt.Printf("Warning: failed to marshal %s: %v\n", pkg, err)
			continue
		}

		writer.Write(data)
		count++
	}

	// Write JSON array end
	writer.WriteString("\n]\n")

	fmt.Printf("Successfully generated metadata for %d packages\n", count)
	return nil
}
