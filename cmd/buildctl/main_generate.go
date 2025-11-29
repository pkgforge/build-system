package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/pkgforge/build-system/internal/metadata"
	"github.com/pkgforge/build-system/internal/queue"
	"github.com/pkgforge/build-system/pkg/models"
	"github.com/spf13/cobra"
)

func generateCmd() *cobra.Command {
	var outputDir string
	var genBincache bool
	var genPkgcache bool
	var arch string
	var parallel int

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate metadata files (INDEX.json, bincache, pkgcache)",
		Long: `Generate metadata files from build results and GHCR packages.

This command can generate:
  - INDEX.json with build statistics
  - bincache metadata (JSON, SQLite, compressed formats)
  - pkgcache metadata (JSON, SQLite, compressed formats)

Examples:
  # Generate just INDEX.json from build results
  buildctl generate --output artifacts

  # Generate bincache metadata for x86_64
  buildctl generate --output artifacts --bincache --arch x86_64-Linux

  # Generate both bincache and pkgcache
  buildctl generate --output artifacts --bincache --pkgcache --arch x86_64-Linux
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			// Create output directory
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				return fmt.Errorf("failed to create output directory: %w", err)
			}

			// Get statistics
			stats, err := qm.GetStats()
			if err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}

			// Get all successful builds
			successfulBuilds, err := qm.List(models.StatusSucceeded, 0)
			if err != nil {
				return fmt.Errorf("failed to list successful builds: %w", err)
			}

			// Generate INDEX.json
			indexData := map[string]interface{}{
				"version":      "1.0.0",
				"generated_at": time.Now().UTC().Format(time.RFC3339),
				"statistics": map[string]interface{}{
					"total_builds":    stats.TotalBuilds,
					"succeeded":       stats.Succeeded,
					"failed":          stats.Failed,
					"queued":          stats.Queued,
					"building":        stats.Building,
					"cancelled":       stats.Cancelled,
					"success_rate":    stats.SuccessRate,
					"avg_duration_seconds": stats.AvgDuration,
				},
				"builds": buildList(successfulBuilds),
			}

			indexPath := fmt.Sprintf("%s/INDEX.json", outputDir)
			if err := writeJSON(indexPath, indexData); err != nil {
				return fmt.Errorf("failed to write INDEX.json: %w", err)
			}

			fmt.Printf("Generated: %s\n", indexPath)

			// Generate stats.json (for build history)
			statsPath := fmt.Sprintf("%s/stats.json", outputDir)
			if err := writeJSON(statsPath, stats); err != nil {
				return fmt.Errorf("failed to write stats.json: %w", err)
			}

			fmt.Printf("Generated: %s\n", statsPath)

			fmt.Printf("\nBuild metadata generation complete!\n")
			fmt.Printf("  - Total builds: %d\n", stats.TotalBuilds)
			fmt.Printf("  - Succeeded: %d\n", stats.Succeeded)
			fmt.Printf("  - Failed: %d\n", stats.Failed)
			fmt.Printf("  - Success rate: %.2f%%\n", stats.SuccessRate)

			// Generate bincache metadata if requested
			if genBincache {
				fmt.Println("\n" + strings.Repeat("=", 50))
				fmt.Println("Generating bincache metadata...")
				fmt.Println(strings.Repeat("=", 50))

				gen := metadata.NewGenerator(metadata.GeneratorConfig{
					Arch:       arch,
					OutputDir:  outputDir,
					SoarqlPath: "/usr/local/bin/soarql",
					Parallel:   parallel,
					Type:       "bincache",
				})

				if err := gen.Generate(); err != nil {
					return fmt.Errorf("failed to generate bincache metadata: %w", err)
				}
			}

			// Generate pkgcache metadata if requested
			if genPkgcache {
				fmt.Println("\n" + strings.Repeat("=", 50))
				fmt.Println("Generating pkgcache metadata...")
				fmt.Println(strings.Repeat("=", 50))

				gen := metadata.NewGenerator(metadata.GeneratorConfig{
					Arch:       arch,
					OutputDir:  outputDir,
					SoarqlPath: "/usr/local/bin/soarql",
					Parallel:   parallel,
					Type:       "pkgcache",
				})

				if err := gen.Generate(); err != nil {
					return fmt.Errorf("failed to generate pkgcache metadata: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outputDir, "output", "./artifacts", "Output directory for metadata files")
	cmd.Flags().BoolVar(&genBincache, "bincache", false, "Generate bincache metadata")
	cmd.Flags().BoolVar(&genPkgcache, "pkgcache", false, "Generate pkgcache metadata")
	cmd.Flags().StringVar(&arch, "arch", "x86_64-Linux", "Architecture for metadata generation")
	cmd.Flags().IntVar(&parallel, "parallel", runtime.NumCPU(), "Number of parallel workers")

	return cmd
}

func buildList(builds []models.Build) []map[string]interface{} {
	var result []map[string]interface{}

	for _, build := range builds {
		item := map[string]interface{}{
			"id":           build.ID,
			"pkg_name":     build.PkgName,
			"pkg_id":       build.PkgID,
			"arch":         build.Arch,
			"recipe_path":  build.RecipePath,
			"created_at":   build.CreatedAt.Format(time.RFC3339),
		}

		if build.StartedAt != nil {
			item["started_at"] = build.StartedAt.Format(time.RFC3339)
		}
		if build.CompletedAt != nil {
			item["completed_at"] = build.CompletedAt.Format(time.RFC3339)
		}
		if build.DurationSecs != nil {
			item["duration_seconds"] = *build.DurationSecs
		}
		if build.BuildLogURL != "" {
			item["build_log_url"] = build.BuildLogURL
		}

		result = append(result, item)
	}

	return result
}

func writeJSON(path string, data interface{}) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}
