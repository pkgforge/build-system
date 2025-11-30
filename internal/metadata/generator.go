package metadata

import (
	"fmt"
	"os"
	"path/filepath"
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
	config       GeneratorConfig
	ghcrPackages []GHCRPackage // Optional pre-fetched packages
}

// NewGenerator creates a new metadata generator
func NewGenerator(config GeneratorConfig) *Generator {
	return &Generator{config: config}
}

// WithGHCRPackages sets pre-fetched GHCR packages to avoid redundant API calls
func (g *Generator) WithGHCRPackages(packages []GHCRPackage) *Generator {
	g.ghcrPackages = packages
	return g
}

// Generate runs the full metadata generation pipeline
func (g *Generator) Generate() error {
	fmt.Printf("Starting metadata generation for %s (%s)\n", g.config.Type, g.config.Arch)

	// Step 1: Verify soarql is available
	if !fileExists(g.config.SoarqlPath) {
		return fmt.Errorf("soarql not found at %s - please install it first", g.config.SoarqlPath)
	}

	// Step 2: Get GHCR packages (fetch if not already provided)
	var ghcrPackages []GHCRPackage
	if len(g.ghcrPackages) > 0 {
		fmt.Printf("Using pre-fetched GHCR package list (%d packages)\n", len(g.ghcrPackages))
		ghcrPackages = g.ghcrPackages
	} else {
		fmt.Println("Fetching GHCR package list...")
		var err error
		ghcrPackages, err = FetchGHCRPackages()
		if err != nil {
			return fmt.Errorf("failed to fetch GHCR packages: %w", err)
		}
		fmt.Printf("Found %d total GHCR packages\n", len(ghcrPackages))
	}

	// Step 3: Filter packages based on type
	var packages []string
	if g.config.Type == "bincache" {
		packages = FilterBincachePackages(ghcrPackages)
	} else if g.config.Type == "pkgcache" {
		packages = FilterPkgcachePackages(ghcrPackages)
	} else {
		return fmt.Errorf("invalid type: %s (must be 'bincache' or 'pkgcache')", g.config.Type)
	}

	fmt.Printf("Filtered to %d %s packages\n", len(packages), g.config.Type)

	if len(packages) == 0 {
		return fmt.Errorf("no packages found for %s", g.config.Type)
	}

	// Step 4: Create output directory
	outputDir := filepath.Join(g.config.OutputDir, g.config.Type, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Step 5: Generate JSON metadata using soarql
	jsonPath := filepath.Join(outputDir, fmt.Sprintf("%s.json", g.config.Arch))
	soarqlConfig := SoarqlConfig{
		SoarqlPath: g.config.SoarqlPath,
		Arch:       g.config.Arch,
		WorkDir:    "/tmp",
	}

	fmt.Println("Generating metadata from GHCR packages...")
	if err := GenerateMetadataForPackages(soarqlConfig, packages, jsonPath, g.config.Parallel); err != nil {
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
