package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkgforge/build-system/pkg/models"
	"gopkg.in/yaml.v3"
)

// Scanner handles scanning SBUILD recipes
type Scanner struct {
	repoPath string
}

// New creates a new scanner
func New(repoPath string) *Scanner {
	return &Scanner{repoPath: repoPath}
}

// ScanAll scans all SBUILD recipes in the repository
func (s *Scanner) ScanAll() ([]models.Recipe, error) {
	var recipes []models.Recipe

	// Scan binaries directory
	binariesPath := filepath.Join(s.repoPath, "binaries")
	binaries, err := s.scanDirectory(binariesPath, "binaries")
	if err != nil {
		return nil, fmt.Errorf("failed to scan binaries: %w", err)
	}
	recipes = append(recipes, binaries...)

	// Scan packages directory
	packagesPath := filepath.Join(s.repoPath, "packages")
	packages, err := s.scanDirectory(packagesPath, "packages")
	if err != nil {
		return nil, fmt.Errorf("failed to scan packages: %w", err)
	}
	recipes = append(recipes, packages...)

	return recipes, nil
}

// scanDirectory scans a directory for SBUILD recipes
func (s *Scanner) scanDirectory(dir, category string) ([]models.Recipe, error) {
	var recipes []models.Recipe

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip disabled files
		if strings.HasSuffix(path, ".disabled") {
			return nil
		}

		// Only process .yaml files
		if !info.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			recipe, err := s.parseRecipe(path, category)
			if err != nil {
				// Log error but continue scanning
				fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", path, err)
				return nil
			}
			recipes = append(recipes, *recipe)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return recipes, nil
}

// parseRecipe parses a single SBUILD recipe file
func (s *Scanner) parseRecipe(path, category string) (*models.Recipe, error) {
	// Calculate relative path from repo root
	relPath, err := filepath.Rel(s.repoPath, path)
	if err != nil {
		relPath = path
	}

	// Extract package name from filename
	// Format: binaries/p/package-name.yaml or packages/p/package-name/variant.yaml
	base := filepath.Base(path)
	pkgName := strings.TrimSuffix(base, filepath.Ext(base))

	// For packages in subdirectories, use directory name
	dir := filepath.Dir(path)
	parentDir := filepath.Base(dir)
	if category == "packages" && parentDir != "packages" {
		pkgName = parentDir
	}

	// Try to parse YAML for metadata (best effort - ignore errors)
	recipe := models.Recipe{
		PkgID:       pkgName,
		Name:        pkgName,
		BuildScript: relPath,
		FilePath:    path,
	}

	// Attempt to extract metadata from YAML (soft fail)
	data, err := os.ReadFile(path)
	if err == nil {
		var yamlData map[string]interface{}
		if yaml.Unmarshal(data, &yamlData) == nil {
			// Try to extract pkg field first (simpler name like "btop")
			if pkg, ok := yamlData["pkg"].(string); ok && pkg != "" {
				recipe.Name = pkg
			}

			// Try to extract pkg_id (full ID like "github.com.aristocratos.btop")
			if pkgID, ok := yamlData["pkg_id"].(string); ok && pkgID != "" {
				recipe.PkgID = pkgID
				// If we didn't get a simpler name from "pkg", use pkg_id
				if recipe.Name == pkgName {
					recipe.Name = pkgID
				}
			}

			// pkg_name overrides if present
			if pkgNameField, ok := yamlData["pkg_name"].(string); ok && pkgNameField != "" {
				recipe.Name = pkgNameField
			}

			if version, ok := yamlData["version"].(string); ok {
				recipe.Version = version
			}
			if desc, ok := yamlData["description"].(string); ok {
				recipe.Description = desc
			}
			if homepage, ok := yamlData["homepage"].(string); ok {
				recipe.Homepage = homepage
			}
			if srcURL, ok := yamlData["src_url"].(string); ok {
				recipe.SourceURL = srcURL
			}
		}
	}

	return &recipe, nil
}

// ScanByPackage scans for a specific package
func (s *Scanner) ScanByPackage(pkgName string) (*models.Recipe, error) {
	recipes, err := s.ScanAll()
	if err != nil {
		return nil, err
	}

	// Try to match by various fields
	for _, recipe := range recipes {
		if recipe.PkgID == pkgName ||
			recipe.Name == pkgName ||
			strings.Contains(recipe.PkgID, pkgName) ||
			strings.Contains(recipe.BuildScript, "/"+pkgName+"/") {
			return &recipe, nil
		}
	}

	return nil, fmt.Errorf("package not found: %s", pkgName)
}

// GetRecipeCount returns the count of recipes by category
func (s *Scanner) GetRecipeCount() (binaries, packages int, err error) {
	binariesPath := filepath.Join(s.repoPath, "binaries")
	binaries, err = s.countRecipes(binariesPath)
	if err != nil {
		return 0, 0, err
	}

	packagesPath := filepath.Join(s.repoPath, "packages")
	packages, err = s.countRecipes(packagesPath)
	if err != nil {
		return 0, 0, err
	}

	return binaries, packages, nil
}

// countRecipes counts recipes in a directory
func (s *Scanner) countRecipes(dir string) (int, error) {
	count := 0

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && !strings.HasSuffix(path, ".disabled") &&
			(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			count++
		}

		return nil
	})

	return count, err
}
