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
	config GeneratorConfig
}

// NewGenerator creates a new metadata generator
func NewGenerator(config GeneratorConfig) *Generator {
	return &Generator{config: config}
}

// Generate runs the full metadata generation pipeline
func (g *Generator) Generate() error {
	fmt.Printf("Starting metadata generation for %s (%s)\n", g.config.Type, g.config.Arch)

	// Step 1: Fetch package list from SBUILD_LIST.json (with release asset fallback)
	var packages []string
	var err error

	fmt.Printf("Fetching %s package list...\n", g.config.Type)

	if g.config.Type == "bincache" {
		packages, err = FetchPackagesFromSBuildList(BincacheReleaseURL, BincacheFallbackURL)
	} else if g.config.Type == "pkgcache" {
		packages, err = FetchPackagesFromSBuildList(PkgcacheReleaseURL, PkgcacheFallbackURL)
	} else {
		return fmt.Errorf("invalid type: %s (must be 'bincache' or 'pkgcache')", g.config.Type)
	}

	if err != nil {
		return fmt.Errorf("failed to fetch package list: %w", err)
	}

	fmt.Printf("Found %d %s packages\n", len(packages), g.config.Type)

	if len(packages) == 0 {
		return fmt.Errorf("no packages found for %s", g.config.Type)
	}

	// Step 4: Create output directory
	outputDir := filepath.Join(g.config.OutputDir, g.config.Type, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Step 5: Generate JSON metadata using oras
	jsonPath := filepath.Join(outputDir, fmt.Sprintf("%s.json", g.config.Arch))
	fetchConfig := FetchConfig{
		OrasPath: "oras", // Use oras from PATH
		Arch:     g.config.Arch,
		WorkDir:  "/tmp",
	}

	fmt.Println("Generating metadata from GHCR packages...")
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
