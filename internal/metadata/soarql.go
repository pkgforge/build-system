package metadata

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Category    string   `json:"category"`
	Checksum    string   `json:"checksum"`
	DownloadURL string   `json:"download_url"`
	GHCRPkg     string   `json:"ghcr_pkg"`
	Homepage    string   `json:"homepage"`
	Icon        string   `json:"icon"`
	License     string   `json:"license"`
	Maintainer  string   `json:"maintainer"`
	Note        string   `json:"note"`
	ProvidesPkg []string `json:"provides_pkg"`
	Repology    string   `json:"repology"`
	SrcURL      string   `json:"src_url"`
	Tag         string   `json:"tag"`
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

// QueryPackageMetadata uses oras to fetch metadata for a package from GHCR
func QueryPackageMetadata(config FetchConfig, ghcrPkg string) (*PackageMetadata, error) {
	// Construct GHCR image reference
	// Input format: "bincache/40four/official" or just "40four/official"
	// Output format: "ghcr.io/pkgforge/bincache/40four/official:x86_64-Linux"

	var imageRef string
	if strings.HasPrefix(ghcrPkg, "ghcr.io/") {
		// Already has full prefix
		imageRef = fmt.Sprintf("%s:%s", ghcrPkg, config.Arch)
	} else {
		// Add ghcr.io/pkgforge/ prefix if needed
		imageRef = fmt.Sprintf("ghcr.io/pkgforge/%s:%s", ghcrPkg, config.Arch)
	}

	// Create temporary directory for package extraction
	tmpDir, err := os.MkdirTemp(config.WorkDir, "pkg-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Pull package from GHCR using oras
	cmd := exec.Command(config.OrasPath, "pull", imageRef)
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		// Package might not exist or no access - return nil to skip
		return nil, nil
	}

	// Look for .METADATA or metadata.json file
	metadataFiles := []string{".METADATA", "metadata.json", ".INFO"}
	var metaFile string
	for _, name := range metadataFiles {
		path := filepath.Join(tmpDir, name)
		if _, err := os.Stat(path); err == nil {
			metaFile = path
			break
		}
	}

	if metaFile == "" {
		// No metadata file found, construct basic metadata from package info
		return constructBasicMetadata(ghcrPkg, config.Arch), nil
	}

	// Read and parse metadata file
	data, err := os.ReadFile(metaFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var meta PackageMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		// If parsing fails, try to extract basic info
		return constructBasicMetadata(ghcrPkg, config.Arch), nil
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
	for i, pkg := range packages {
		if i%100 == 0 {
			fmt.Printf("Progress: %d/%d packages...\n", i, len(packages))
		}

		meta, err := QueryPackageMetadata(config, pkg)
		if err != nil {
			fmt.Printf("Warning: failed to query %s: %v\n", pkg, err)
			continue
		}

		if meta == nil {
			// Package has no metadata, skip
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
