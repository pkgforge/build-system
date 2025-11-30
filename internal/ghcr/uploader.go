package ghcr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkgforge/build-system/pkg/models"
	"gopkg.in/yaml.v3"
)

// Uploader handles uploading packages to GHCR
type Uploader struct {
	orasPath string
}

// PackageInfo holds metadata extracted from recipe or generated files
type PackageInfo struct {
	Pkg         string   `json:"pkg" yaml:"pkg"`             // Simple package name
	PkgName     string   `json:"pkg_name" yaml:"pkg_name"`   // Display name
	PkgFamily   string   `json:"pkg_family" yaml:"pkg_family"`
	Version     string   `json:"version" yaml:"version"`
	Description string   `json:"description" yaml:"description"`
	Homepage    string   `json:"homepage" yaml:"homepage"`
	SrcURL      string   `json:"src_url" yaml:"src_url"`
	Provides    []string `json:"provides" yaml:"provides"`
	BSum        string   `json:"bsum,omitempty"`
	ShaSum      string   `json:"shasum,omitempty"`
	Size        string   `json:"size,omitempty"`
	SizeRaw     int64    `json:"size_raw,omitempty"`
	BuildDate   string   `json:"build_date,omitempty"`
}

// NewUploader creates a new GHCR uploader
func NewUploader() *Uploader {
	return &Uploader{
		orasPath: "oras",
	}
}

// UploadPackage uploads a built package directory to GHCR
// If the package provides multiple binaries, it uploads each one separately
func (u *Uploader) UploadPackage(build *models.Build, pkgDir string) error {
	// Check if package directory exists
	if _, err := os.Stat(pkgDir); os.IsNotExist(err) {
		return fmt.Errorf("package directory not found: %s", pkgDir)
	}

	// Extract package metadata from recipe and generated files
	pkgInfo, err := u.extractPackageInfo(build, pkgDir)
	if err != nil {
		return fmt.Errorf("failed to extract package info: %w", err)
	}

	// If version is missing, use a default
	if pkgInfo.Version == "" {
		pkgInfo.Version = fmt.Sprintf("latest-%s", time.Now().UTC().Format("20060102"))
	}

	// Find all files in the package directory
	files, err := filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no files found in package directory: %s", pkgDir)
	}

	// Generate metadata JSON if it doesn't exist
	if err := u.generateMetadataJSON(pkgInfo, pkgDir, build); err != nil {
		fmt.Printf("    ⚠ Warning: Failed to generate metadata JSON: %v\n", err)
	}

	// Sign all package files with minisign before uploading
	if err := u.signPackageFiles(files); err != nil {
		fmt.Printf("    ⚠ Warning: Failed to sign package files: %v\n", err)
		fmt.Printf("    Continuing upload without signatures...\n")
	}

	// Re-scan directory to include .sig and .json files
	files, err = filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files after signing: %w", err)
	}

	// Determine if we should upload for each provided binary
	// If provides array has multiple items, upload once for each
	// Otherwise, upload once with the main package name
	uploadTargets := u.determineUploadTargets(pkgInfo)

	if len(uploadTargets) == 0 {
		return fmt.Errorf("no upload targets determined (no pkg, provides, pkg_name, or pkg_family)")
	}

	// Upload for each target
	var uploadErrors []string
	successCount := 0

	for i, targetName := range uploadTargets {
		if len(uploadTargets) > 1 {
			fmt.Printf("    [%d/%d] Uploading variant: %s\n", i+1, len(uploadTargets), targetName)
		}

		if err := u.uploadSinglePackage(build, pkgDir, pkgInfo, targetName, files); err != nil {
			errMsg := fmt.Sprintf("failed to upload %s: %v", targetName, err)
			uploadErrors = append(uploadErrors, errMsg)
			fmt.Printf("    ✗ %s\n", errMsg)
		} else {
			successCount++
		}
	}

	// Return error if all uploads failed
	if successCount == 0 {
		return fmt.Errorf("all uploads failed: %v", strings.Join(uploadErrors, "; "))
	}

	// Warn if some uploads failed
	if len(uploadErrors) > 0 {
		fmt.Printf("    ⚠ Warning: %d/%d uploads succeeded, %d failed\n", successCount, len(uploadTargets), len(uploadErrors))
	}

	fmt.Printf("    ✓ Successfully uploaded %d package(s)\n", successCount)
	return nil
}

// determineUploadTargets determines which package names to upload
// If provides has multiple items, return all of them
// Otherwise, return the single best package name
func (u *Uploader) determineUploadTargets(pkgInfo *PackageInfo) []string {
	// If provides has multiple items, upload for each one
	if len(pkgInfo.Provides) > 1 {
		return pkgInfo.Provides
	}

	// Otherwise, use priority: pkg > provides[0] > pkg_name > pkg_family
	var targetName string
	if pkgInfo.Pkg != "" {
		targetName = pkgInfo.Pkg
	} else if len(pkgInfo.Provides) == 1 && pkgInfo.Provides[0] != "" {
		targetName = pkgInfo.Provides[0]
	} else if pkgInfo.PkgName != "" {
		targetName = pkgInfo.PkgName
	} else if pkgInfo.PkgFamily != "" {
		targetName = pkgInfo.PkgFamily
	}

	if targetName != "" {
		return []string{targetName}
	}

	return []string{}
}

// uploadSinglePackage uploads a single package variant to GHCR
func (u *Uploader) uploadSinglePackage(build *models.Build, pkgDir string, pkgInfo *PackageInfo, targetName string, files []string) error {
	// Determine repository based on recipe path
	repo := u.determineRepo(build.RecipePath)

	// Extract build type from recipe filename (e.g., "static/official", "appimage/official/stable")
	buildType := u.extractBuildType(build.RecipePath)

	// Sanitize package name for GHCR (replace dots with hyphens, keep lowercase)
	pkgNameSanitized := u.sanitizePackageName(targetName)

	// Sanitize pkg_family as well
	pkgFamilySanitized := u.sanitizePackageName(pkgInfo.PkgFamily)

	// Normalize architecture (convert to lowercase)
	archNormalized := strings.ToLower(build.Arch)

	// Sanitize version (replace invalid characters)
	versionSanitized := u.sanitizeVersion(pkgInfo.Version)

	// Construct GHCR image name
	// Format: ghcr.io/pkgforge/{repo}/{pkg_family}/{build_type}/{pkg_name}:{version}-{arch}
	imageName := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s:%s-%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	fmt.Printf("    Uploading to GHCR: %s\n", imageName)

	// Build oras push command with all files and annotations
	args := u.buildOrasPushCommand(imageName, pkgInfo, build, targetName)

	// Add all files from the package directory as relative paths
	for _, file := range files {
		// Skip directories
		fileInfo, err := os.Stat(file)
		if err != nil || fileInfo.IsDir() {
			continue
		}
		// Use just the filename (will be relative since we set cmd.Dir)
		args = append(args, filepath.Base(file))
	}

	cmd := exec.Command(u.orasPath, args...)
	cmd.Dir = pkgDir // Change to package directory so paths are relative
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to push to GHCR: %w", err)
	}

	fmt.Printf("    ✓ Successfully uploaded to %s\n", imageName)
	return nil
}

// extractPackageInfo extracts package metadata from recipe file and generated JSON
func (u *Uploader) extractPackageInfo(build *models.Build, pkgDir string) (*PackageInfo, error) {
	pkgInfo := &PackageInfo{
		PkgName:   build.PkgName,
		BuildDate: time.Now().UTC().Format(time.RFC3339),
	}

	// Try to read generated metadata JSON first (highest priority)
	jsonFiles, _ := filepath.Glob(filepath.Join(pkgDir, "*.json"))
	for _, jsonFile := range jsonFiles {
		if strings.HasSuffix(jsonFile, ".sig") {
			continue // Skip signature files
		}
		data, err := os.ReadFile(jsonFile)
		if err != nil {
			continue
		}
		var metadata map[string]interface{}
		if err := json.Unmarshal(data, &metadata); err == nil {
			// Extract fields from JSON metadata
			if v, ok := metadata["pkg"].(string); ok && v != "" {
				pkgInfo.Pkg = v
			}
			if v, ok := metadata["pkg_name"].(string); ok && v != "" {
				pkgInfo.PkgName = v
			}
			if v, ok := metadata["pkg_family"].(string); ok && v != "" {
				pkgInfo.PkgFamily = v
			}
			if v, ok := metadata["version"].(string); ok && v != "" {
				pkgInfo.Version = v
			}
			if v, ok := metadata["description"].(string); ok {
				pkgInfo.Description = v
			}
			if v, ok := metadata["homepage"].(string); ok {
				pkgInfo.Homepage = v
			}
			if v, ok := metadata["src_url"].(string); ok {
				pkgInfo.SrcURL = v
			}
			if v, ok := metadata["provides"].([]interface{}); ok {
				for _, p := range v {
					if s, ok := p.(string); ok {
						pkgInfo.Provides = append(pkgInfo.Provides, s)
					}
				}
			}
			if v, ok := metadata["bsum"].(string); ok {
				pkgInfo.BSum = v
			}
			if v, ok := metadata["shasum"].(string); ok {
				pkgInfo.ShaSum = v
			}
			if v, ok := metadata["size"].(string); ok {
				pkgInfo.Size = v
			}
			if v, ok := metadata["size_raw"].(float64); ok {
				pkgInfo.SizeRaw = int64(v)
			}
			if v, ok := metadata["build_date"].(string); ok && v != "" {
				pkgInfo.BuildDate = v
			}
			break // Use first valid JSON found
		}
	}

	// If no JSON metadata found, try to read from recipe YAML
	if pkgInfo.Version == "" || pkgInfo.PkgFamily == "" {
		recipeData, err := os.ReadFile(build.RecipePath)
		if err == nil {
			var recipe map[string]interface{}
			if err := yaml.Unmarshal(recipeData, &recipe); err == nil {
				if v, ok := recipe["pkg"].(string); ok && pkgInfo.Pkg == "" {
					pkgInfo.Pkg = v
				}
				if v, ok := recipe["pkg_name"].(string); ok && pkgInfo.PkgName == "" {
					pkgInfo.PkgName = v
				}
				if v, ok := recipe["pkg_family"].(string); ok && pkgInfo.PkgFamily == "" {
					pkgInfo.PkgFamily = v
				}
				if v, ok := recipe["version"].(string); ok && pkgInfo.Version == "" {
					pkgInfo.Version = v
				}
				if v, ok := recipe["description"].(string); ok && pkgInfo.Description == "" {
					pkgInfo.Description = v
				}
				if v, ok := recipe["homepage"].(string); ok && pkgInfo.Homepage == "" {
					pkgInfo.Homepage = v
				}
				if v, ok := recipe["src_url"].(string); ok && pkgInfo.SrcURL == "" {
					pkgInfo.SrcURL = v
				}
			}
		}
	}

	// Fallback: extract pkg_family from recipe path if still empty
	if pkgInfo.PkgFamily == "" {
		pkgInfo.PkgFamily, _ = u.extractPackageNames(build.RecipePath)
	}

	return pkgInfo, nil
}

// generateMetadataJSON creates a metadata JSON file for the package
// This generates a separate JSON for each target (to handle multiple provides)
func (u *Uploader) generateMetadataJSON(pkgInfo *PackageInfo, pkgDir string, build *models.Build) error {
	// Check if metadata JSON already exists
	jsonFiles, _ := filepath.Glob(filepath.Join(pkgDir, "*.json"))
	if len(jsonFiles) > 0 {
		for _, jsonFile := range jsonFiles {
			if !strings.HasSuffix(jsonFile, ".sig") {
				// Metadata JSON already exists, skip generation
				return nil
			}
		}
	}

	// Determine upload targets to generate JSON for each
	uploadTargets := u.determineUploadTargets(pkgInfo)

	if len(uploadTargets) == 0 {
		return fmt.Errorf("no upload targets found")
	}

	// Generate JSON for each target
	for _, targetName := range uploadTargets {
		if err := u.generateSingleMetadataJSON(pkgInfo, pkgDir, build, targetName); err != nil {
			return err
		}
	}

	return nil
}

// generateSingleMetadataJSON generates metadata JSON for a single package variant
func (u *Uploader) generateSingleMetadataJSON(pkgInfo *PackageInfo, pkgDir string, build *models.Build, targetName string) error {
	// Determine repository and build type
	repo := u.determineRepo(build.RecipePath)
	buildType := u.extractBuildType(build.RecipePath)

	// Sanitize names
	pkgNameSanitized := u.sanitizePackageName(targetName)
	pkgFamilySanitized := u.sanitizePackageName(pkgInfo.PkgFamily)
	versionSanitized := u.sanitizeVersion(pkgInfo.Version)
	archNormalized := strings.ToLower(build.Arch)

	// Construct GHCR URLs
	ghcrPkg := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s:%s-%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	ghcrURL := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized)

	// API URLs
	downloadURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&download=%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized, targetName)

	manifestURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&manifest",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	metadataURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&download=%s.json",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized, targetName)

	buildLogURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&download=%s.log",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized, targetName)

	// GitHub Actions URL
	buildGHA := ""
	if build.ID > 0 {
		buildGHA = fmt.Sprintf("https://github.com/pkgforge/%s/actions/runs/%d", repo, build.ID)
	}

	// Package webpage
	pkgWebpage := fmt.Sprintf("https://pkgs.pkgforge.dev/repo/%s/%s/%s/%s/%s",
		repo, archNormalized, pkgFamilySanitized, buildType, pkgNameSanitized)

	// Build comprehensive metadata
	metadata := map[string]interface{}{
		"pkg":          targetName,
		"pkg_name":     targetName,
		"pkg_family":   pkgInfo.PkgFamily,
		"pkg_id":       build.PkgID,
		"pkg_type":     buildType,
		"pkg_webpage":  pkgWebpage,
		"version":      pkgInfo.Version,
		"description":  pkgInfo.Description,
		"homepage":     pkgInfo.Homepage,
		"src_url":      pkgInfo.SrcURL,
		"provides":     pkgInfo.Provides,
		"build_date":   pkgInfo.BuildDate,
		"build_id":     fmt.Sprintf("%d", build.ID),
		"build_gha":    buildGHA,
		"build_log":    buildLogURL,
		"build_script": build.RecipePath,
		"host":         build.Arch,
		"ghcr_pkg":     ghcrPkg,
		"ghcr_url":     "https://" + ghcrURL,
		"download_url": downloadURL,
		"manifest_url": manifestURL,
		"metadata_url": metadataURL,
	}

	// Add checksums if available
	if pkgInfo.BSum != "" {
		metadata["bsum"] = pkgInfo.BSum
	}
	if pkgInfo.ShaSum != "" {
		metadata["shasum"] = pkgInfo.ShaSum
	}
	if pkgInfo.Size != "" {
		metadata["size"] = pkgInfo.Size
	}
	if pkgInfo.SizeRaw > 0 {
		metadata["size_raw"] = pkgInfo.SizeRaw
	}

	// Write JSON to file
	jsonPath := filepath.Join(pkgDir, fmt.Sprintf("%s.json", targetName))
	jsonData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata JSON: %w", err)
	}

	fmt.Printf("    ✓ Generated metadata JSON: %s\n", filepath.Base(jsonPath))
	return nil
}

// buildOrasPushCommand builds the oras push command with annotations
func (u *Uploader) buildOrasPushCommand(imageName string, pkgInfo *PackageInfo, build *models.Build, targetName string) []string {
	args := []string{
		"push",
		"--disable-path-validation",
		"--config", "/dev/null:application/vnd.oci.empty.v1+json",
	}

	// Add OCI standard annotations
	args = append(args,
		"--annotation", fmt.Sprintf("org.opencontainers.image.created=%s", pkgInfo.BuildDate),
		"--annotation", fmt.Sprintf("org.opencontainers.image.version=%s", pkgInfo.Version),
		"--annotation", fmt.Sprintf("org.opencontainers.image.title=%s", targetName),
		"--annotation", fmt.Sprintf("org.opencontainers.image.description=%s", pkgInfo.Description),
		"--annotation", "org.opencontainers.image.vendor=pkgforge",
		"--annotation", "org.opencontainers.image.licenses=blessing",
		"--annotation", "org.opencontainers.image.authors=https://docs.pkgforge.dev/contact/chat",
	)

	if pkgInfo.Homepage != "" {
		args = append(args, "--annotation", fmt.Sprintf("org.opencontainers.image.url=%s", pkgInfo.Homepage))
	}
	if pkgInfo.SrcURL != "" {
		args = append(args, "--annotation", fmt.Sprintf("org.opencontainers.image.source=%s", pkgInfo.SrcURL))
	}

	// Add custom pkgforge annotations
	args = append(args,
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.pkg=%s", targetName),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.pkg_name=%s", targetName),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.pkg_family=%s", pkgInfo.PkgFamily),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.version=%s", pkgInfo.Version),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.build_date=%s", pkgInfo.BuildDate),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.build_id=%d", build.ID),
		"--annotation", fmt.Sprintf("dev.pkgforge.soar.description=%s", pkgInfo.Description),
	)

	if pkgInfo.Homepage != "" {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.homepage=%s", pkgInfo.Homepage))
	}
	if pkgInfo.SrcURL != "" {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.src_url=%s", pkgInfo.SrcURL))
	}
	if pkgInfo.BSum != "" {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.bsum=%s", pkgInfo.BSum))
	}
	if pkgInfo.ShaSum != "" {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.shasum=%s", pkgInfo.ShaSum))
	}
	if pkgInfo.Size != "" {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.size=%s", pkgInfo.Size))
	}
	if pkgInfo.SizeRaw > 0 {
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.size_raw=%d", pkgInfo.SizeRaw))
	}
	if len(pkgInfo.Provides) > 0 {
		providesJSON, _ := json.Marshal(pkgInfo.Provides)
		args = append(args, "--annotation", fmt.Sprintf("dev.pkgforge.soar.provides=%s", string(providesJSON)))
	}

	// Add Discord link
	args = append(args, "--annotation", "dev.pkgforge.discord=https://discord.gg/djJUs48Zbu")

	// Add the image name
	args = append(args, imageName)

	return args
}

// extractBuildType extracts build type from recipe filename
// Example: "static.official.stable.yaml" -> "static/official/stable"
func (u *Uploader) extractBuildType(recipePath string) string {
	base := filepath.Base(recipePath)
	// Remove .yaml extension
	name := strings.TrimSuffix(base, filepath.Ext(base))
	// Split by dots and join with slashes
	parts := strings.Split(name, ".")
	return strings.Join(parts, "/")
}

// determineRepo determines if package goes to bincache or pkgcache
func (u *Uploader) determineRepo(recipePath string) string {
	if strings.Contains(recipePath, "binaries/") {
		return "bincache"
	} else if strings.Contains(recipePath, "packages/") {
		return "pkgcache"
	}
	// Default to bincache for unknown paths
	return "bincache"
}

// extractPackageNames extracts package family and name from recipe path
// Example: "binaries/btop/static.official.stable.yaml" -> ("btop", "btop")
// Example: "packages/firefox/appimage.official.stable.yaml" -> ("firefox", "firefox")
func (u *Uploader) extractPackageNames(recipePath string) (family, name string) {
	// Get the directory containing the recipe
	dir := filepath.Dir(recipePath)

	// Extract the package name from directory
	// For paths like "binaries/btop" or "packages/firefox"
	parts := strings.Split(dir, string(filepath.Separator))

	if len(parts) >= 2 {
		name = parts[len(parts)-1]
		family = name
	} else {
		// Fallback: use recipe filename without extension
		base := filepath.Base(recipePath)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		family = name
	}

	return family, name
}

// signPackageFiles signs all files with minisign before upload
func (u *Uploader) signPackageFiles(files []string) error {
	// Check if minisign is available
	if _, err := exec.LookPath("minisign"); err != nil {
		return fmt.Errorf("minisign not found in PATH")
	}

	// Check if private key is in environment variable
	keyContent := os.Getenv("MINISIGN_KEY_CONTENT")
	if keyContent == "" {
		return fmt.Errorf("MINISIGN_KEY_CONTENT environment variable not set")
	}

	// Create temporary key file
	tmpKey, err := os.CreateTemp("", "minisign-*.key")
	if err != nil {
		return fmt.Errorf("failed to create temp key file: %w", err)
	}
	defer os.Remove(tmpKey.Name())

	if _, err := tmpKey.WriteString(keyContent); err != nil {
		tmpKey.Close()
		return fmt.Errorf("failed to write key content: %w", err)
	}
	tmpKey.Close()

	// Sign each file
	signedCount := 0
	for _, file := range files {
		// Skip directories
		fileInfo, err := os.Stat(file)
		if err != nil || fileInfo.IsDir() {
			continue
		}

		// Skip existing .sig files
		if strings.HasSuffix(file, ".sig") {
			continue
		}

		// Sign the file
		// -S = sign mode
		// -s = secret key file
		// -m = message file to sign
		// -x = signature output file (use .sig extension)
		sigFile := file + ".sig"
		cmd := exec.Command("minisign", "-S", "-s", tmpKey.Name(), "-m", file, "-x", sigFile)

		// If password is provided, pipe it to stdin
		password := os.Getenv("MINISIGN_PASSWORD")
		if password != "" {
			cmd.Stdin = strings.NewReader(password + "\n")
		}

		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("    ⚠ Failed to sign %s: %v\n", filepath.Base(file), err)
			fmt.Printf("    Output: %s\n", string(output))
			continue
		}

		signedCount++
	}

	fmt.Printf("    ✓ Signed %d package files with minisign\n", signedCount)
	return nil
}

// sanitizePackageName sanitizes package name for GHCR repository path
// GHCR/OCI registry naming rules:
// - Must be lowercase
// - Can contain: lowercase letters, digits, separators (period, underscores, dashes)
// - But periods have restrictions in repository path components
// Replace dots with hyphens for safety
func (u *Uploader) sanitizePackageName(name string) string {
	if name == "" {
		return name
	}

	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace dots with hyphens (dots cause issues in repository paths)
	name = strings.ReplaceAll(name, ".", "-")

	// Replace any other invalid characters with hyphens
	// Valid characters: a-z, 0-9, _, -
	result := strings.Builder{}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			result.WriteRune(ch)
		} else {
			result.WriteRune('-')
		}
	}

	// Remove leading/trailing separators
	sanitized := strings.Trim(result.String(), "-_")

	// Replace multiple consecutive separators with single hyphen
	for strings.Contains(sanitized, "--") || strings.Contains(sanitized, "__") || strings.Contains(sanitized, "_-") || strings.Contains(sanitized, "-_") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
		sanitized = strings.ReplaceAll(sanitized, "_-", "-")
		sanitized = strings.ReplaceAll(sanitized, "-_", "-")
	}

	return sanitized
}

// sanitizeVersion sanitizes version string for GHCR tag
// OCI tag naming rules:
// - Can contain: lowercase and uppercase letters, digits, underscores, periods, hyphens
// - Cannot start with period or hyphen
func (u *Uploader) sanitizeVersion(version string) string {
	if version == "" {
		return version
	}

	// Replace invalid characters with underscores
	// Valid: A-Z, a-z, 0-9, _, ., -
	result := strings.Builder{}
	for _, ch := range version {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' {
			result.WriteRune(ch)
		} else {
			result.WriteRune('_')
		}
	}

	sanitized := result.String()

	// Remove leading periods or hyphens
	sanitized = strings.TrimLeft(sanitized, ".-")

	// Ensure it's not empty after sanitization
	if sanitized == "" {
		sanitized = "latest"
	}

	return sanitized
}
