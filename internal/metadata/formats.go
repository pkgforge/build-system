package metadata

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// GenerateCompressedFormats creates xz, zstd, and bsum variants
func GenerateCompressedFormats(jsonPath, arch string) error {
	baseDir := filepath.Dir(jsonPath)
	baseName := fmt.Sprintf("%s.json", arch)

	// Generate .xz
	xzPath := filepath.Join(baseDir, fmt.Sprintf("%s.xz", arch))
	fmt.Printf("Generating %s...\n", xzPath)
	if err := runCommand("xz", "-9", "-f", "-k", filepath.Join(baseDir, baseName)); err != nil {
		return fmt.Errorf("failed to create xz: %w", err)
	}

	// Generate .zstd
	zstdPath := filepath.Join(baseDir, fmt.Sprintf("%s.zstd", arch))
	fmt.Printf("Generating %s...\n", zstdPath)
	if err := runCommand("zstd", "-19", "-f", filepath.Join(baseDir, baseName), "-o", zstdPath); err != nil {
		return fmt.Errorf("failed to create zstd: %w", err)
	}

	// Generate b3sum checksums
	fmt.Println("Generating checksums...")
	files := []string{
		filepath.Join(baseDir, baseName),
		filepath.Join(baseDir, fmt.Sprintf("%s.xz", arch)),
		filepath.Join(baseDir, fmt.Sprintf("%s.zstd", arch)),
	}

	for _, file := range files {
		bsumPath := file + ".bsum"
		output, err := runCommandWithOutput("b3sum", file)
		if err != nil {
			// b3sum might not be available, skip
			fmt.Printf("Warning: b3sum not available for %s\n", file)
			continue
		}

		if err := os.WriteFile(bsumPath, []byte(output), 0644); err != nil {
			return fmt.Errorf("failed to write bsum: %w", err)
		}
	}

	return nil
}

// ConvertJSONToSQLite converts JSON metadata to SQLite database
func ConvertJSONToSQLite(jsonPath, dbPath string) error {
	fmt.Printf("Converting %s to SQLite database...\n", jsonPath)

	// Read JSON
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("failed to read JSON: %w", err)
	}

	var packages []PackageMetadata
	if err := json.Unmarshal(data, &packages); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Create SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Create table
	schema := `
	CREATE TABLE IF NOT EXISTS packages (
		pkg TEXT PRIMARY KEY,
		pkg_id TEXT,
		description TEXT,
		version TEXT,
		size TEXT,
		bsum TEXT,
		shasum TEXT,
		build_date TEXT,
		build_id TEXT,
		build_script TEXT,
		category TEXT,
		checksum TEXT,
		download_url TEXT,
		ghcr_pkg TEXT,
		homepage TEXT,
		icon TEXT,
		license TEXT,
		maintainer TEXT,
		note TEXT,
		provides_pkg TEXT,
		repology TEXT,
		src_url TEXT,
		tag TEXT,
		web_url TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_pkg_id ON packages(pkg_id);
	CREATE INDEX IF NOT EXISTS idx_ghcr_pkg ON packages(ghcr_pkg);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Insert packages
	stmt, err := db.Prepare(`
		INSERT OR REPLACE INTO packages (
			pkg, pkg_id, description, version, size, bsum, shasum,
			build_date, build_id, build_script, category, checksum,
			download_url, ghcr_pkg, homepage, icon, license, maintainer,
			note, provides_pkg, repology, src_url, tag, web_url
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, pkg := range packages {
		// Convert array fields to JSON strings
		provides, _ := json.Marshal(pkg.Provides)
		category, _ := json.Marshal(pkg.Category)
		homepage, _ := json.Marshal(pkg.Homepage)
		license, _ := json.Marshal(pkg.License)
		maintainer, _ := json.Marshal(pkg.Maintainer)
		note, _ := json.Marshal(pkg.Note)
		repology, _ := json.Marshal(pkg.Repology)
		srcURL, _ := json.Marshal(pkg.SrcURL)
		tag, _ := json.Marshal(pkg.Tag)

		_, err := stmt.Exec(
			pkg.Pkg, pkg.PkgID, pkg.Description, pkg.Version, pkg.Size,
			pkg.Bsum, pkg.Shasum, pkg.BuildDate, pkg.BuildID, pkg.BuildScript,
			string(category), pkg.Shasum, pkg.DownloadURL, pkg.GHCRPkg,
			string(homepage), pkg.Icon, string(license), string(maintainer), string(note),
			string(provides), string(repology), string(srcURL), string(tag), pkg.PkgWebpage,
		)
		if err != nil {
			fmt.Printf("Warning: failed to insert %s: %v\n", pkg.Pkg, err)
		}
	}

	fmt.Printf("Inserted %d packages into database\n", len(packages))

	// Optimize database
	if _, err := db.Exec("VACUUM"); err != nil {
		fmt.Printf("Warning: failed to vacuum database: %v\n", err)
	}

	return nil
}

// GenerateAllFormats generates all format variants
func GenerateAllFormats(jsonPath, arch string) error {
	baseDir := filepath.Dir(jsonPath)

	// Generate SQLite database
	dbPath := filepath.Join(baseDir, fmt.Sprintf("%s.db", arch))
	if err := ConvertJSONToSQLite(jsonPath, dbPath); err != nil {
		return err
	}

	// Generate compressed formats for JSON
	if err := GenerateCompressedFormats(jsonPath, arch); err != nil {
		return err
	}

	// Generate compressed formats for DB
	dbBaseName := fmt.Sprintf("%s.db", arch)
	dbFiles := []string{
		filepath.Join(baseDir, dbBaseName),
	}

	// Compress DB
	fmt.Println("Compressing database...")
	if err := runCommand("xz", "-9", "-f", "-k", filepath.Join(baseDir, dbBaseName)); err != nil {
		fmt.Printf("Warning: failed to create db.xz: %v\n", err)
	}

	if err := runCommand("zstd", "-19", "-f", filepath.Join(baseDir, dbBaseName), "-o", filepath.Join(baseDir, fmt.Sprintf("%s.db.zstd", arch))); err != nil {
		fmt.Printf("Warning: failed to create db.zstd: %v\n", err)
	}

	// Generate checksums for DB files
	dbFiles = append(dbFiles,
		filepath.Join(baseDir, fmt.Sprintf("%s.db.xz", arch)),
		filepath.Join(baseDir, fmt.Sprintf("%s.db.zstd", arch)),
	)

	for _, file := range dbFiles {
		if !fileExists(file) {
			continue
		}

		bsumPath := file + ".bsum"
		output, err := runCommandWithOutput("b3sum", file)
		if err != nil {
			continue
		}

		if err := os.WriteFile(bsumPath, []byte(output), 0644); err != nil {
			fmt.Printf("Warning: failed to write bsum for %s: %v\n", file, err)
		}
	}

	return nil
}
