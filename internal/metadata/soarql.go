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

// SoarqlConfig holds configuration for soarql
type SoarqlConfig struct {
	SoarqlPath string
	Arch       string
	WorkDir    string
}

// InstallSoarql downloads and installs the soarql binary
func InstallSoarql(installPath string) error {
	fmt.Println("Installing soarql...")

	// Determine architecture
	arch, err := runCommandWithOutput("uname", "-m")
	if err != nil {
		return fmt.Errorf("failed to detect architecture: %w", err)
	}
	arch = strings.TrimSpace(arch)

	// Download from nightly release directly
	downloadURL := fmt.Sprintf("https://github.com/pkgforge/soarql/releases/download/nightly/soarql-%s-linux", arch)
	fmt.Printf("Downloading soarql from: %s\n", downloadURL)

	// Download
	tmpFile := "/tmp/soarql"
	if err := runCommand("curl", "-qfsSL", downloadURL, "-o", tmpFile); err != nil {
		return fmt.Errorf("failed to download soarql: %w", err)
	}

	// Install
	if err := runCommand("chmod", "+x", tmpFile); err != nil {
		return err
	}

	if err := copyFile(tmpFile, installPath); err != nil {
		return fmt.Errorf("failed to install soarql: %w", err)
	}

	// Verify
	output, err := runCommandWithOutput(installPath, "--version")
	if err != nil {
		return fmt.Errorf("soarql installation verification failed: %w", err)
	}

	fmt.Printf("Installed soarql: %s\n", strings.TrimSpace(output))
	return nil
}

// QueryPackageMetadata uses soarql to fetch metadata for a package
func QueryPackageMetadata(config SoarqlConfig, ghcrPkg string) (*PackageMetadata, error) {
	// soarql expects package name without ghcr.io/pkgforge/ prefix
	pkgName := strings.TrimPrefix(ghcrPkg, "ghcr.io/pkgforge/")

	// Run soarql to get metadata
	cmd := exec.Command(config.SoarqlPath,
		"--pkg", pkgName,
		"--arch", config.Arch,
		"--format", "json")

	cmd.Dir = config.WorkDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Some packages might not have metadata, skip them
		return nil, nil
	}

	var meta PackageMetadata
	if err := json.Unmarshal(output, &meta); err != nil {
		// If parsing fails, return nil (package might not have proper metadata)
		return nil, nil
	}

	return &meta, nil
}

// GenerateMetadataForPackages processes a list of packages and generates metadata
func GenerateMetadataForPackages(config SoarqlConfig, packages []string, outputPath string, parallel int) error {
	fmt.Printf("Processing %d packages with %d parallel workers...\n", len(packages), parallel)

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
