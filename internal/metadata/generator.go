package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GeneratorConfig holds configuration for metadata generation
type GeneratorConfig struct {
	Arch       string
	OutputDir  string
	SoarqlPath string
	Parallel   int
	Type       string // "bincache" or "pkgcache"
}

// Generator handles metadata generation
type Generator struct {
	config GeneratorConfig
}

// NewGenerator creates a new metadata generator
func NewGenerator(config GeneratorConfig) *Generator {
	return &Generator{config: config}
}

// Generate runs the full metadata generation pipeline
func (g *Generator) Generate() error {
	fmt.Printf("Starting metadata generation for %s (%s)\n", g.config.Type, g.config.Arch)

	// Step 1: Download GHCR package list from releases (no API calls)
	fmt.Println("Downloading GHCR package list...")
	allPackages, err := FetchGHCRPackageList()
	if err != nil {
		return fmt.Errorf("failed to fetch GHCR packages: %w", err)
	}

	// Step 2: Fetch SBUILD_LIST to get package families
	fmt.Printf("Fetching %s SBUILD_LIST...\n", g.config.Type)
	var sbuildURL, fallbackURL string
	if g.config.Type == "bincache" {
		sbuildURL, fallbackURL = BincacheReleaseURL, BincacheFallbackURL
	} else if g.config.Type == "pkgcache" {
		sbuildURL, fallbackURL = PkgcacheReleaseURL, PkgcacheFallbackURL
	} else {
		return fmt.Errorf("invalid type: %s (must be 'bincache' or 'pkgcache')", g.config.Type)
	}

	sbuildFamilies, err := FetchPackagesFromSBuildList(sbuildURL, fallbackURL)
	if err != nil {
		return fmt.Errorf("failed to fetch SBUILD_LIST: %w", err)
	}

	// Step 3: Filter GHCR packages to match type and SBUILD families
	var packages []string
	for _, pkg := range allPackages {
		// Check if package is of the right type (bincache/pkgcache)
		if !strings.HasPrefix(pkg, g.config.Type+"/") {
			continue
		}

		// Check if package matches any SBUILD family
		for _, family := range sbuildFamilies {
			// family is like "bincache/a-utils/official"
			// pkg is like "bincache/a-utils/official/cal"
			if strings.HasPrefix(pkg, family+"/") || pkg == family {
				packages = append(packages, pkg)
				break
			}
		}
	}

	fmt.Printf("Found %d %s packages matching SBUILD_LIST\n", len(packages), g.config.Type)

	if len(packages) == 0 {
		return fmt.Errorf("no packages found for %s", g.config.Type)
	}

	// Step 4: Create output directory
	outputDir := filepath.Join(g.config.OutputDir, g.config.Type, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Step 5: Generate JSON metadata from GHCR manifests
	jsonPath := filepath.Join(outputDir, fmt.Sprintf("%s.json", g.config.Arch))
	fetchConfig := FetchConfig{
		OrasPath: "oras", // Use oras from PATH
		Arch:     g.config.Arch,
		WorkDir:  "/tmp",
	}

	fmt.Println("Generating metadata from GHCR package manifests...")
	if err := GenerateMetadataForPackages(fetchConfig, packages, jsonPath, g.config.Parallel); err != nil {
		return fmt.Errorf("failed to generate metadata: %w", err)
	}

	// Step 6: Generate all format variants
	fmt.Println("Generating format variants...")
	if err := GenerateAllFormats(jsonPath, g.config.Arch); err != nil {
		return fmt.Errorf("failed to generate formats: %w", err)
	}

	fmt.Printf("\n✅ Metadata generation complete for %s (%s)\n", g.config.Type, g.config.Arch)
	fmt.Printf("Output directory: %s\n", outputDir)

	// List generated files
	fmt.Println("\nGenerated files:")
	files, _ := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("%s.*", g.config.Arch)))
	for _, file := range files {
		info, err := os.Stat(file)
		if err == nil {
			fmt.Printf("  - %s (%d bytes)\n", filepath.Base(file), info.Size())
		}
	}

	return nil
}
