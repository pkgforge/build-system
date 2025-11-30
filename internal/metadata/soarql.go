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
	// Basic info
	Disabled        string   `json:"_disabled"`
	Host            string   `json:"host"`
	Rank            string   `json:"rank"`
	Pkg             string   `json:"pkg"`
	PkgFamily       string   `json:"pkg_family"`
	PkgID           string   `json:"pkg_id"`
	PkgName         string   `json:"pkg_name"`
	PkgType         string   `json:"pkg_type"`
	PkgWebpage      string   `json:"pkg_webpage"`

	// App info
	AppID           string   `json:"app_id"`
	Appstream       string   `json:"appstream"`
	Desktop         string   `json:"desktop"`

	// Descriptive
	Category        []string `json:"category"`
	Description     string   `json:"description"`
	Homepage        []string `json:"homepage"`
	Icon            string   `json:"icon"`
	License         []string `json:"license"`
	Maintainer      []string `json:"maintainer"`
	Note            []string `json:"note"`
	Provides        []string `json:"provides"`
	Repology        []string `json:"repology"`
	Screenshots     []string `json:"screenshots"`
	SrcURL          []string `json:"src_url"`
	Tag             []string `json:"tag"`

	// Version
	Version         string   `json:"version"`
	VersionUpstream string   `json:"version_upstream"`

	// Build info
	Bsum            string   `json:"bsum"`
	BuildDate       string   `json:"build_date"`
	BuildGHA        string   `json:"build_gha"`
	BuildID         string   `json:"build_id"`
	BuildLog        string   `json:"build_log"`
	BuildScript     string   `json:"build_script"`

	// Download
	DownloadURL     string   `json:"download_url"`
	GHCRPkg         string   `json:"ghcr_pkg"`
	GHCRURL         string   `json:"ghcr_url"`
	ManifestURL     string   `json:"manifest_url"`
	Shasum          string   `json:"shasum"`
	Size            string   `json:"size"`
	SizeRaw         string   `json:"size_raw"`
	Snapshots       []string `json:"snapshots"`
}

// FetchConfig holds configuration for metadata fetching
type FetchConfig struct {
	OrasPath string
	Arch     string
	WorkDir  string
}

// ensureGHCRLogin ensures oras is logged in to GHCR
func ensureGHCRLogin(orasPath string) error {
	token := os.Getenv("GHCR_TOKEN")
	if token == "" {
		return nil
	}

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
	var pkgRef string
	if strings.HasPrefix(ghcrPkg, "ghcr.io/") {
		pkgRef = ghcrPkg
	} else {
		pkgRef = fmt.Sprintf("ghcr.io/pkgforge/%s", ghcrPkg)
	}

	cmd := exec.Command(config.OrasPath, "repo", "tags", pkgRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		pullErrorCount++
		if pullErrorCount <= 3 {
			fmt.Printf("    ⚠ Failed to list tags for %s: %v\n", pkgRef, err)
		}
		return nil, nil
	}

	var latestTag string
	archPattern := strings.ToLower(config.Arch)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		tag := strings.TrimSpace(scanner.Text())
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

	metaJSON, ok := manifest.Annotations["dev.pkgforge.soar.json"]
	if !ok {
		return nil, nil
	}

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

// constructBasicMetadata creates basic metadata when annotation is not available
func constructBasicMetadata(pkgName, arch string) *PackageMetadata {
	parts := strings.Split(strings.TrimPrefix(pkgName, "ghcr.io/pkgforge/"), "/")

	var name string
	if len(parts) >= 2 {
		name = parts[len(parts)-2]
	} else if len(parts) > 0 {
		name = parts[0]
	} else {
		name = pkgName
	}

	return &PackageMetadata{
		Pkg:     name,
		GHCRPkg: pkgName,
		Host:    arch,
	}
}

// GenerateMetadataForPackages processes a list of packages and generates metadata
func GenerateMetadataForPackages(config FetchConfig, packages []string, outputPath string, parallel int) error {
	fmt.Printf("Processing %d packages with %d parallel workers...\n", len(packages), parallel)

	if err := ensureGHCRLogin(config.OrasPath); err != nil {
		return fmt.Errorf("failed to authenticate with GHCR: %w", err)
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	defer writer.Flush()

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
			errorCount++
			continue
		}

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

	writer.WriteString("\n]\n")

	fmt.Printf("Successfully generated metadata for %d packages\n", count)
	return nil
}
