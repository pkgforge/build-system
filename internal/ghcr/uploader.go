package ghcr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkgforge/build-system/pkg/models"
)

// Uploader handles uploading packages to GHCR
type Uploader struct {
	orasPath string
}

// NewUploader creates a new GHCR uploader
func NewUploader() *Uploader {
	return &Uploader{
		orasPath: "oras",
	}
}

// UploadPackage uploads a built package directory to GHCR
func (u *Uploader) UploadPackage(build *models.Build, pkgDir string) error {
	// Check if package directory exists
	if _, err := os.Stat(pkgDir); os.IsNotExist(err) {
		return fmt.Errorf("package directory not found: %s", pkgDir)
	}

	// Find all files in the package directory
	files, err := filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no files found in package directory: %s", pkgDir)
	}

	// Sign all package files with minisign before uploading
	if err := u.signPackageFiles(files); err != nil {
		fmt.Printf("    ⚠ Warning: Failed to sign package files: %v\n", err)
		fmt.Printf("    Continuing upload without signatures...\n")
		// Don't fail the upload if signing fails - just warn
	}

	// Re-scan directory to include .minisig files
	files, err = filepath.Glob(filepath.Join(pkgDir, "*"))
	if err != nil {
		return fmt.Errorf("failed to list package files after signing: %w", err)
	}

	// Determine repository based on recipe path
	repo := u.determineRepo(build.RecipePath)

	// Extract package family from path
	pkgFamily, _ := u.extractPackageNames(build.RecipePath)

	// Extract build type from recipe filename (e.g., "static", "appimage", "official")
	buildType := u.extractBuildType(build.RecipePath)

	// Construct GHCR image name
	// Format: ghcr.io/pkgforge/{repo}/{pkg_family}/{build_type}:{arch}
	imageName := fmt.Sprintf("ghcr.io/pkgforge/%s/%s/%s:%s", repo, pkgFamily, buildType, build.Arch)

	fmt.Printf("    Uploading to GHCR: %s\n", imageName)

	// Build oras push command with all files
	args := []string{
		"push",
		"--artifact-type", "application/vnd.pkgforge.package.v1+binary",
		imageName,
	}

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
