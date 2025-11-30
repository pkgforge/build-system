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

// Uploader handles uploading packages to GHCR using ORAS
type Uploader struct {
	orasPath string
}

// PackageInfo holds metadata extracted from recipe files or generated during build
type PackageInfo struct {
	Pkg             string      `json:"pkg" yaml:"pkg"`
	PkgName         string      `json:"pkg_name" yaml:"pkg_name"`
	PkgFamily       string      `json:"pkg_family" yaml:"pkg_family"`
	PkgID           string      `json:"pkg_id" yaml:"pkg_id"`
	Version         string      `json:"version" yaml:"version"`
	VersionUpstream string      `json:"version_upstream,omitempty" yaml:"version_upstream,omitempty"`
	Description     string      `json:"description" yaml:"description"`
	Homepage        interface{} `json:"homepage" yaml:"homepage"`
	SrcURL          interface{} `json:"src_url" yaml:"src_url"`
	Provides        []string    `json:"provides" yaml:"provides"`
	Category        interface{} `json:"category,omitempty" yaml:"category,omitempty"`
	License         interface{} `json:"license,omitempty" yaml:"license,omitempty"`
	Maintainer      interface{} `json:"maintainer,omitempty" yaml:"maintainer,omitempty"`
	Note            interface{} `json:"note,omitempty" yaml:"note,omitempty"`
	Tag             interface{} `json:"tag,omitempty" yaml:"tag,omitempty"`
	Repology        interface{} `json:"repology,omitempty" yaml:"repology,omitempty"`
	Screenshots     interface{} `json:"screenshots,omitempty" yaml:"screenshots,omitempty"`
	Icon            string      `json:"icon,omitempty" yaml:"icon,omitempty"`
	Desktop         string      `json:"desktop,omitempty" yaml:"desktop,omitempty"`
	AppID           string      `json:"app_id,omitempty" yaml:"app_id,omitempty"`
	Appstream       string      `json:"appstream,omitempty" yaml:"appstream,omitempty"`
	BSum            string      `json:"bsum,omitempty"`
	ShaSum          string      `json:"shasum,omitempty"`
	Size            string      `json:"size,omitempty"`
	SizeRaw         int64       `json:"size_raw,omitempty"`
	BuildDate       string      `json:"build_date,omitempty"`
	Rank            string      `json:"rank,omitempty" yaml:"rank,omitempty"`
	Disabled        string      `json:"_disabled,omitempty" yaml:"_disabled,omitempty"`
}

// NewUploader creates a new GHCR uploader
func NewUploader() *Uploader {
	return &Uploader{
		orasPath: "oras",
	}
}

// UploadPackage uploads a built package directory to GHCR.
// For packages with multiple binaries, each is uploaded separately.
func (u *Uploader) UploadPackage(build *models.Build, pkgDir string) error {
	if _, err := os.Stat(pkgDir); os.IsNotExist(err) {
		return fmt.Errorf("package directory not found: %s", pkgDir)
	}

	pkgInfo, err := u.extractPackageInfo(build, pkgDir)
	if err != nil {
		return fmt.Errorf("failed to extract package info: %w", err)
	}

	if pkgInfo.Version == "" {
		pkgInfo.Version = fmt.Sprintf("latest-%s", time.Now().UTC().Format("20060102"))
	}

	files, err := filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in package directory: %s", pkgDir)
	}

	if err := u.generateMetadataJSON(pkgInfo, pkgDir, build); err != nil {
		fmt.Printf("    ⚠ Warning: Failed to generate metadata JSON: %v\n", err)
	}

	if err := u.signPackageFiles(files); err != nil {
		fmt.Printf("    ⚠ Warning: Failed to sign package files: %v\n", err)
		fmt.Printf("    Continuing upload without signatures...\n")
	}

	files, err = filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files after signing: %w", err)
	}

	uploadTargets := u.determineUploadTargets(pkgInfo)
	if len(uploadTargets) == 0 {
		return fmt.Errorf("no upload targets determined")
	}

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

	if successCount == 0 {
		return fmt.Errorf("all uploads failed: %v", strings.Join(uploadErrors, "; "))
	}

	if len(uploadErrors) > 0 {
		fmt.Printf("    ⚠ Warning: %d/%d uploads succeeded, %d failed\n", successCount, len(uploadTargets), len(uploadErrors))
	}

	fmt.Printf("    ✓ Successfully uploaded %d package(s)\n", successCount)
	return nil
}

// determineUploadTargets returns package names to upload.
// For multi-binary packages, returns all provided binaries.
// For single packages, returns the best available name.
func (u *Uploader) determineUploadTargets(pkgInfo *PackageInfo) []string {
	if len(pkgInfo.Provides) > 1 {
		return pkgInfo.Provides
	}

	var targetName string
	switch {
	case len(pkgInfo.Provides) == 1 && pkgInfo.Provides[0] != "":
		targetName = pkgInfo.Provides[0]
	case pkgInfo.PkgName != "":
		targetName = pkgInfo.PkgName
	case pkgInfo.PkgFamily != "":
		targetName = pkgInfo.PkgFamily
	case pkgInfo.Pkg != "":
		targetName = pkgInfo.Pkg
		if idx := strings.LastIndex(targetName, "."); idx > 0 {
			baseName := targetName[:idx]
			if !strings.Contains(baseName, ".") {
				targetName = baseName
			}
		}
	}

	if targetName != "" {
		return []string{targetName}
	}
	return []string{}
}

// uploadSinglePackage uploads a specific package variant to GHCR.
// Includes only the target binary plus shared files (not other binaries).
func (u *Uploader) uploadSinglePackage(build *models.Build, pkgDir string, pkgInfo *PackageInfo, targetName string, files []string) error {
	repo := u.determineRepo(build.RecipePath)
	buildType := u.extractBuildType(build.RecipePath)

	pkgNameSanitized := u.sanitizePackageName(targetName)
	pkgFamilySanitized := u.sanitizePackageName(pkgInfo.PkgFamily)
	archNormalized := strings.ToLower(build.Arch)
	versionSanitized := u.sanitizeVersion(pkgInfo.Version)

	imageName := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s:%s-%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	fmt.Printf("    Uploading to GHCR: %s\n", imageName)

	args := u.buildOrasPushCommand(imageName, pkgInfo, build, targetName)

	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if err != nil || fileInfo.IsDir() {
			continue
		}

		fileName := filepath.Base(file)

		isOtherBinary := false
		if len(pkgInfo.Provides) > 1 {
			for _, providedBinary := range pkgInfo.Provides {
				if fileName == providedBinary && providedBinary != targetName && !strings.Contains(fileName, ".") {
					isOtherBinary = true
					break
				}
			}
		}

		if !isOtherBinary {
			args = append(args, fileName)
		}
	}

	cmd := exec.Command(u.orasPath, args...)
	cmd.Dir = pkgDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to push to GHCR: %w", err)
	}

	fmt.Printf("    ✓ Successfully uploaded to %s\n", imageName)
	return nil
}

// extractPackageInfo extracts metadata from recipe YAML, .version files, and generated JSON
func (u *Uploader) extractPackageInfo(build *models.Build, pkgDir string) (*PackageInfo, error) {
	pkgInfo := &PackageInfo{
		BuildDate: time.Now().UTC().Format(time.RFC3339),
	}

	u.readRecipeYAML(pkgInfo, build.RecipePath)
	u.readVersionFile(pkgInfo, pkgDir)
	u.readGeneratedJSON(pkgInfo, pkgDir)

	if pkgInfo.PkgFamily == "" {
		pkgInfo.PkgFamily, _ = u.extractPackageNames(build.RecipePath)
	}

	if pkgInfo.PkgName == "" && build.PkgName != "" {
		name := build.PkgName
		if strings.Count(name, ".") >= 2 {
			parts := strings.Split(name, ".")
			name = parts[len(parts)-1]
		}
		pkgInfo.PkgName = name
	}

	return pkgInfo, nil
}

func (u *Uploader) readRecipeYAML(pkgInfo *PackageInfo, recipePath string) {
	recipeData, err := os.ReadFile(recipePath)
	if err != nil {
		return
	}

	var recipe map[string]interface{}
	if err := yaml.Unmarshal(recipeData, &recipe); err != nil {
		return
	}

	if v, ok := recipe["pkg"].(string); ok && v != "" {
		pkgInfo.Pkg = v
	}
	if v, ok := recipe["pkg_family"].(string); ok && v != "" {
		pkgInfo.PkgFamily = v
	}
	if v, ok := recipe["pkg_id"].(string); ok && v != "" {
		pkgInfo.PkgID = v
	}
	if v, ok := recipe["version"].(string); ok && v != "" {
		pkgInfo.Version = v
	}
	if v, ok := recipe["version_upstream"].(string); ok && v != "" {
		pkgInfo.VersionUpstream = v
	}
	if v, ok := recipe["description"].(string); ok {
		pkgInfo.Description = v
	}
	if v, ok := recipe["homepage"]; ok && v != nil {
		pkgInfo.Homepage = v
	}
	if v, ok := recipe["src_url"]; ok && v != nil {
		pkgInfo.SrcURL = v
	}
	if v, ok := recipe["provides"].([]interface{}); ok {
		for _, p := range v {
			if s, ok := p.(string); ok {
				pkgInfo.Provides = append(pkgInfo.Provides, s)
			}
		}
	}
	if v, ok := recipe["category"]; ok && v != nil {
		pkgInfo.Category = v
	}
	if v, ok := recipe["license"]; ok && v != nil {
		pkgInfo.License = v
	}
	if v, ok := recipe["maintainer"]; ok && v != nil {
		pkgInfo.Maintainer = v
	}
	if v, ok := recipe["note"]; ok && v != nil {
		pkgInfo.Note = v
	}
	if v, ok := recipe["tag"]; ok && v != nil {
		pkgInfo.Tag = v
	}
}

func (u *Uploader) readVersionFile(pkgInfo *PackageInfo, pkgDir string) {
	if pkgInfo.Version != "" {
		return
	}

	versionFiles, _ := filepath.Glob(filepath.Join(pkgDir, "*.version"))
	for _, versionFile := range versionFiles {
		data, err := os.ReadFile(versionFile)
		if err == nil {
			version := strings.TrimSpace(string(data))
			if version != "" {
				pkgInfo.Version = version
				break
			}
		}
	}
}

func (u *Uploader) readGeneratedJSON(pkgInfo *PackageInfo, pkgDir string) {
	jsonFiles, _ := filepath.Glob(filepath.Join(pkgDir, "*.json"))
	for _, jsonFile := range jsonFiles {
		if strings.HasSuffix(jsonFile, ".sig") {
			continue
		}
		data, err := os.ReadFile(jsonFile)
		if err != nil {
			continue
		}
		var metadata map[string]interface{}
		if err := json.Unmarshal(data, &metadata); err == nil {
			if v, ok := metadata["bsum"].(string); ok && pkgInfo.BSum == "" {
				pkgInfo.BSum = v
			}
			if v, ok := metadata["shasum"].(string); ok && pkgInfo.ShaSum == "" {
				pkgInfo.ShaSum = v
			}
			if v, ok := metadata["size"].(string); ok && pkgInfo.Size == "" {
				pkgInfo.Size = v
			}
			if v, ok := metadata["size_raw"].(float64); ok && pkgInfo.SizeRaw == 0 {
				pkgInfo.SizeRaw = int64(v)
			}
			if v, ok := metadata["icon"].(string); ok && pkgInfo.Icon == "" {
				pkgInfo.Icon = v
			}
			if v, ok := metadata["desktop"].(string); ok && pkgInfo.Desktop == "" {
				pkgInfo.Desktop = v
			}
			if v, ok := metadata["app_id"].(string); ok && pkgInfo.AppID == "" {
				pkgInfo.AppID = v
			}
			if v, ok := metadata["appstream"].(string); ok && pkgInfo.Appstream == "" {
				pkgInfo.Appstream = v
			}
			if v, ok := metadata["rank"].(string); ok && pkgInfo.Rank == "" {
				pkgInfo.Rank = v
			}
			if v, ok := metadata["_disabled"].(string); ok && pkgInfo.Disabled == "" {
				pkgInfo.Disabled = v
			}
			if v, ok := metadata["repology"]; ok && v != nil && pkgInfo.Repology == nil {
				pkgInfo.Repology = v
			}
			if v, ok := metadata["screenshots"]; ok && v != nil && pkgInfo.Screenshots == nil {
				pkgInfo.Screenshots = v
			}
			break
		}
	}
}

// generateMetadataJSON creates a metadata JSON file for each package variant
func (u *Uploader) generateMetadataJSON(pkgInfo *PackageInfo, pkgDir string, build *models.Build) error {
	jsonFiles, _ := filepath.Glob(filepath.Join(pkgDir, "*.json"))
	if len(jsonFiles) > 0 {
		for _, jsonFile := range jsonFiles {
			if !strings.HasSuffix(jsonFile, ".sig") {
				return nil
			}
		}
	}

	uploadTargets := u.determineUploadTargets(pkgInfo)
	if len(uploadTargets) == 0 {
		return fmt.Errorf("no upload targets found")
	}

	for _, targetName := range uploadTargets {
		if err := u.generateSingleMetadataJSON(pkgInfo, pkgDir, build, targetName); err != nil {
			return err
		}
	}

	return nil
}

// generateSingleMetadataJSON generates metadata JSON for a single package variant
func (u *Uploader) generateSingleMetadataJSON(pkgInfo *PackageInfo, pkgDir string, build *models.Build, targetName string) error {
	repo := u.determineRepo(build.RecipePath)
	buildType := u.extractBuildType(build.RecipePath)

	pkgNameSanitized := u.sanitizePackageName(targetName)
	pkgFamilySanitized := u.sanitizePackageName(pkgInfo.PkgFamily)
	versionSanitized := u.sanitizeVersion(pkgInfo.Version)
	archNormalized := strings.ToLower(build.Arch)

	ghcrPkg := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s:%s-%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	ghcrURL := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s/%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized)

	downloadURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&download=%s",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized, targetName)

	manifestURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&manifest",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized)

	buildLogURL := fmt.Sprintf("https://api.ghcr.pkgforge.dev/pkgforge/%s/%s/%s/%s?tag=%s-%s&download=%s.log",
		repo, pkgFamilySanitized, buildType, pkgNameSanitized, versionSanitized, archNormalized, targetName)

	buildGHA := ""
	if build.ID > 0 {
		buildGHA = fmt.Sprintf("https://github.com/pkgforge/%s/actions/runs/%d", repo, build.ID)
	}

	pkgWebpage := fmt.Sprintf("https://pkgs.pkgforge.dev/repo/%s/%s/%s/%s",
		repo, archNormalized, pkgFamilySanitized, pkgNameSanitized)

	metadata := map[string]interface{}{
		"_disabled":        pkgInfo.Disabled,
		"host":             build.Arch,
		"rank":             pkgInfo.Rank,
		"pkg":              pkgInfo.Pkg,
		"pkg_family":       pkgInfo.PkgFamily,
		"pkg_id":           pkgInfo.PkgID,
		"pkg_name":         targetName,
		"pkg_type":         buildType,
		"pkg_webpage":      pkgWebpage,
		"app_id":           pkgInfo.AppID,
		"appstream":        pkgInfo.Appstream,
		"category":         pkgInfo.Category,
		"description":      pkgInfo.Description,
		"desktop":          pkgInfo.Desktop,
		"homepage":         pkgInfo.Homepage,
		"icon":             pkgInfo.Icon,
		"license":          pkgInfo.License,
		"maintainer":       pkgInfo.Maintainer,
		"provides":         pkgInfo.Provides,
		"note":             pkgInfo.Note,
		"repology":         pkgInfo.Repology,
		"screenshots":      pkgInfo.Screenshots,
		"src_url":          pkgInfo.SrcURL,
		"tag":              pkgInfo.Tag,
		"version":          pkgInfo.Version,
		"version_upstream": pkgInfo.VersionUpstream,
		"bsum":             pkgInfo.BSum,
		"build_date":       pkgInfo.BuildDate,
		"build_gha":        buildGHA,
		"build_id":         fmt.Sprintf("%d", build.ID),
		"build_log":        buildLogURL,
		"build_script":     build.RecipePath,
		"download_url":     downloadURL,
		"ghcr_pkg":         ghcrPkg,
		"ghcr_url":         "https://" + ghcrURL,
		"manifest_url":     manifestURL,
		"shasum":           pkgInfo.ShaSum,
		"size":             pkgInfo.Size,
		"size_raw":         pkgInfo.SizeRaw,
		"snapshots":        []string{},
	}

	cleanMetadata := make(map[string]interface{})
	for k, v := range metadata {
		if v != nil && v != "" && v != 0 && v != int64(0) {
			cleanMetadata[k] = v
		} else if k == "_disabled" || k == "rank" || k == "snapshots" || k == "provides" {
			cleanMetadata[k] = v
		}
	}

	jsonPath := filepath.Join(pkgDir, fmt.Sprintf("%s.json", targetName))
	jsonData, err := json.MarshalIndent(cleanMetadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata JSON: %w", err)
	}

	fmt.Printf("    ✓ Generated metadata JSON: %s\n", filepath.Base(jsonPath))
	return nil
}

// buildOrasPushCommand builds the oras push command with OCI annotations
func (u *Uploader) buildOrasPushCommand(imageName string, pkgInfo *PackageInfo, build *models.Build, targetName string) []string {
	args := []string{
		"push",
		"--disable-path-validation",
		"--config", "/dev/null:application/vnd.oci.empty.v1+json",
		"--annotation", fmt.Sprintf("org.opencontainers.image.created=%s", pkgInfo.BuildDate),
		"--annotation", fmt.Sprintf("org.opencontainers.image.version=%s", pkgInfo.Version),
		"--annotation", fmt.Sprintf("org.opencontainers.image.title=%s", targetName),
		"--annotation", fmt.Sprintf("org.opencontainers.image.description=%s", pkgInfo.Description),
		"--annotation", "org.opencontainers.image.vendor=pkgforge",
		"--annotation", "org.opencontainers.image.licenses=blessing",
		"--annotation", "org.opencontainers.image.authors=https://docs.pkgforge.dev/contact/chat",
	}

	if pkgInfo.Homepage != "" {
		args = append(args, "--annotation", fmt.Sprintf("org.opencontainers.image.url=%s", pkgInfo.Homepage))
	}
	if pkgInfo.SrcURL != "" {
		args = append(args, "--annotation", fmt.Sprintf("org.opencontainers.image.source=%s", pkgInfo.SrcURL))
	}

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

	args = append(args,
		"--annotation", "dev.pkgforge.discord=https://discord.gg/djJUs48Zbu",
		imageName,
	)

	return args
}

// extractBuildType extracts build type from recipe filename
// Example: "static.official.stable.yaml" -> "static/official/stable"
func (u *Uploader) extractBuildType(recipePath string) string {
	base := filepath.Base(recipePath)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(name, ".")
	return strings.Join(parts, "/")
}

// determineRepo determines if package goes to bincache or pkgcache
func (u *Uploader) determineRepo(recipePath string) string {
	if strings.Contains(recipePath, "binaries/") {
		return "bincache"
	}
	if strings.Contains(recipePath, "packages/") {
		return "pkgcache"
	}
	return "bincache"
}

// extractPackageNames extracts package family and name from recipe path
func (u *Uploader) extractPackageNames(recipePath string) (family, name string) {
	dir := filepath.Dir(recipePath)
	parts := strings.Split(dir, string(filepath.Separator))

	if len(parts) >= 2 {
		name = parts[len(parts)-1]
		family = name
	} else {
		base := filepath.Base(recipePath)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		family = name
	}

	return family, name
}

// signPackageFiles signs all files with minisign before upload
func (u *Uploader) signPackageFiles(files []string) error {
	if _, err := exec.LookPath("minisign"); err != nil {
		return fmt.Errorf("minisign not found in PATH")
	}

	keyContent := os.Getenv("MINISIGN_KEY_CONTENT")
	if keyContent == "" {
		return fmt.Errorf("MINISIGN_KEY_CONTENT environment variable not set")
	}

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

	signedCount := 0
	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if err != nil || fileInfo.IsDir() {
			continue
		}

		if strings.HasSuffix(file, ".sig") {
			continue
		}

		sigFile := file + ".sig"
		cmd := exec.Command("minisign", "-S", "-s", tmpKey.Name(), "-m", file, "-x", sigFile)

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

// sanitizePackageName sanitizes package name for GHCR repository path.
// Converts to lowercase, replaces dots with hyphens, removes invalid characters.
func (u *Uploader) sanitizePackageName(name string) string {
	if name == "" {
		return name
	}

	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, ".", "-")

	result := strings.Builder{}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			result.WriteRune(ch)
		} else {
			result.WriteRune('-')
		}
	}

	sanitized := strings.Trim(result.String(), "-_")

	for strings.Contains(sanitized, "--") || strings.Contains(sanitized, "__") || strings.Contains(sanitized, "_-") || strings.Contains(sanitized, "-_") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
		sanitized = strings.ReplaceAll(sanitized, "_-", "-")
		sanitized = strings.ReplaceAll(sanitized, "-_", "-")
	}

	return sanitized
}

// sanitizeVersion sanitizes version string for GHCR tag.
// Removes invalid characters and ensures valid OCI tag format.
func (u *Uploader) sanitizeVersion(version string) string {
	if version == "" {
		return version
	}

	result := strings.Builder{}
	for _, ch := range version {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' {
			result.WriteRune(ch)
		} else {
			result.WriteRune('_')
		}
	}

	sanitized := result.String()
	sanitized = strings.TrimLeft(sanitized, ".-")

	if sanitized == "" {
		sanitized = "latest"
	}

	return sanitized
}
