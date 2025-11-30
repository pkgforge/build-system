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

	fmt.Println("Downloading GHCR package list...")
	allPackages, err := FetchGHCRPackageList()
	if err != nil {
		return fmt.Errorf("failed to fetch GHCR packages: %w", err)
	}

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

	var packages []string
	for _, pkg := range allPackages {
		if !strings.HasPrefix(pkg, g.config.Type+"/") {
			continue
		}

		for _, family := range sbuildFamilies {
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

	outputDir := filepath.Join(g.config.OutputDir, g.config.Type, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	jsonPath := filepath.Join(outputDir, fmt.Sprintf("%s.json", g.config.Arch))
	fetchConfig := FetchConfig{
		OrasPath: "oras",
		Arch:     g.config.Arch,
		WorkDir:  "/tmp",
	}

	fmt.Println("Generating metadata from GHCR package manifests...")
	if err := GenerateMetadataForPackages(fetchConfig, packages, jsonPath, g.config.Parallel); err != nil {
		return fmt.Errorf("failed to generate metadata: %w", err)
	}

	fmt.Println("Generating format variants...")
	if err := GenerateAllFormats(jsonPath, g.config.Arch); err != nil {
		return fmt.Errorf("failed to generate formats: %w", err)
	}

	fmt.Printf("\n✅ Metadata generation complete for %s (%s)\n", g.config.Type, g.config.Arch)
	fmt.Printf("Output directory: %s\n", outputDir)

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
