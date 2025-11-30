package main

import (
	"fmt"
	"os"
	"time"

	"github.com/pkgforge/build-system/internal/executor"
	"github.com/pkgforge/build-system/internal/queue"
	"github.com/pkgforge/build-system/internal/reporter"
	"github.com/pkgforge/build-system/internal/scanner"
	"github.com/pkgforge/build-system/pkg/models"
	"github.com/spf13/cobra"
)

var (
	dbPath      string
	repoPath    string
	arch        string
	pkgName     string
	priority    int
	workers     int
	forceBuild  bool
	all         bool
	limit       int
	sbuildPath  string
	maxDuration int
	buildID     int64
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "buildctl",
		Short: "Build system CLI for PkgForge",
		Long:  "A clean, efficient build orchestration system for pkgforge/soarpkgs",
	}

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "buildqueue.db", "Path to SQLite database")

	rootCmd.AddCommand(syncCmd())
	rootCmd.AddCommand(queueCmd())
	rootCmd.AddCommand(forceCmd())
	rootCmd.AddCommand(buildCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(statsCmd())
	rootCmd.AddCommand(resetCmd())
	rootCmd.AddCommand(cancelCmd())
	rootCmd.AddCommand(listCmd())
	rootCmd.AddCommand(generateCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func syncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync recipes from soarpkgs repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repoPath == "" {
				return fmt.Errorf("--repo is required")
			}

			fmt.Printf("Scanning recipes from: %s\n", repoPath)

			s := scanner.New(repoPath)
			recipes, err := s.ScanAll()
			if err != nil {
				return fmt.Errorf("failed to scan recipes: %w", err)
			}

			fmt.Printf("Found %d recipes\n", len(recipes))

			binaries, packages, err := s.GetRecipeCount()
			if err != nil {
				return fmt.Errorf("failed to get recipe count: %w", err)
			}

			fmt.Printf("  Binaries: %d\n", binaries)
			fmt.Printf("  Packages: %d\n", packages)

			// Save sync state
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			if err := qm.SaveSyncState("soarpkgs", "HEAD", len(recipes)); err != nil {
				return fmt.Errorf("failed to save sync state: %w", err)
			}

			fmt.Println("Sync completed successfully")
			return nil
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "Path to soarpkgs repository (required)")
	cmd.MarkFlagRequired("repo")

	return cmd
}

func queueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Add packages to build queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			if repoPath == "" {
				return fmt.Errorf("--repo is required")
			}

			s := scanner.New(repoPath)

			var recipes []models.Recipe

			if all {
				recipes, err = s.ScanAll()
				if err != nil {
					return fmt.Errorf("failed to scan recipes: %w", err)
				}
			} else if pkgName != "" {
				recipe, err := s.ScanByPackage(pkgName)
				if err != nil {
					return fmt.Errorf("failed to find package: %w", err)
				}
				recipes = []models.Recipe{*recipe}
			} else {
				return fmt.Errorf("either --all or --pkg must be specified")
			}

			// Determine architectures to queue
			arches := []string{"x86_64-Linux"}
			if arch != "" {
				arches = []string{arch}
			} else {
				arches = []string{"x86_64-Linux", "aarch64-Linux", "riscv64-Linux"}
			}

			queued := 0
			for _, recipe := range recipes {
				for _, a := range arches {
					buildID, err := qm.Add(recipe.Name, recipe.PkgID, recipe.BuildScript, a, priority, false)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Failed to queue %s [%s]: %v\n", recipe.PkgID, a, err)
						continue
					}
					queued++
					fmt.Printf("Queued: %s [%s] (build ID: %d)\n", recipe.PkgID, a, buildID)
				}
			}

			fmt.Printf("\nQueued %d builds\n", queued)
			return nil
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "Path to soarpkgs repository (required)")
	cmd.Flags().StringVar(&pkgName, "pkg", "", "Package name to queue")
	cmd.Flags().StringVar(&arch, "arch", "", "Architecture (x86_64-Linux, aarch64-Linux, riscv64-Linux)")
	cmd.Flags().IntVar(&priority, "priority", 10, "Build priority (higher = built first)")
	cmd.Flags().BoolVar(&all, "all", false, "Queue all packages")
	cmd.MarkFlagRequired("repo")

	return cmd
}

func forceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "force",
		Short: "Force build a specific package",
		RunE: func(cmd *cobra.Command, args []string) error {
			if pkgName == "" {
				return fmt.Errorf("--pkg is required")
			}
			if arch == "" {
				arch = "x86_64-Linux"
			}

			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			if repoPath == "" {
				return fmt.Errorf("--repo is required")
			}

			s := scanner.New(repoPath)
			recipe, err := s.ScanByPackage(pkgName)
			if err != nil {
				return fmt.Errorf("failed to find package: %w", err)
			}

			buildID, err := qm.Add(recipe.Name, recipe.PkgID, recipe.BuildScript, arch, 100, true)
			if err != nil {
				return fmt.Errorf("failed to queue force build: %w", err)
			}

			fmt.Printf("Force build queued: %s [%s] (build ID: %d)\n", recipe.PkgID, arch, buildID)
			return nil
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "Path to soarpkgs repository (required)")
	cmd.Flags().StringVar(&pkgName, "pkg", "", "Package name (required)")
	cmd.Flags().StringVar(&arch, "arch", "x86_64-Linux", "Architecture")
	cmd.MarkFlagRequired("pkg")
	cmd.MarkFlagRequired("repo")

	return cmd
}

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show build queue status",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			r := reporter.New(qm)

			if pkgName != "" {
				return r.PrintPackageStatus(pkgName)
			}

			return r.PrintStatus()
		},
	}

	cmd.Flags().StringVar(&pkgName, "pkg", "", "Show status for specific package")

	return cmd
}

func statsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show build statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			r := reporter.New(qm)
			return r.PrintStats()
		},
	}

	return cmd
}

func resetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset/clear build queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			status := ""
			if cmd.Flags().Changed("failed") {
				status = string(models.StatusFailed)
			} else if cmd.Flags().Changed("queued") {
				status = string(models.StatusQueued)
			}

			if err := qm.Clear(models.BuildStatus(status)); err != nil {
				return fmt.Errorf("failed to reset queue: %w", err)
			}

			if status == "" {
				fmt.Println("All builds cleared")
			} else {
				fmt.Printf("Cleared all %s builds\n", status)
			}

			return nil
		},
	}

	cmd.Flags().Bool("failed", false, "Reset only failed builds")
	cmd.Flags().Bool("queued", false, "Reset only queued builds")

	return cmd
}

func cancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel a specific build",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			var buildID int64
			if _, err := fmt.Sscanf(args[0], "%d", &buildID); err != nil {
				return fmt.Errorf("invalid build ID: %s", args[0])
			}

			if err := qm.Cancel(buildID); err != nil {
				return fmt.Errorf("failed to cancel build: %w", err)
			}

			fmt.Printf("Build %d cancelled\n", buildID)
			return nil
		},
	}

	return cmd
}

func buildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Execute builds from queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			// Check if sbuild is installed
			if err := executor.CheckSbuildInstalled(sbuildPath); err != nil {
				return err
			}

			// Get sbuild version
			version, err := executor.GetSbuildVersion(sbuildPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not get sbuild version: %v\n", err)
			} else {
				fmt.Printf("Using sbuild: %s\n", version)
			}

			// Create executor
			exec, err := executor.New(qm, executor.Config{
				SbuildPath: sbuildPath,
				RepoPath:   repoPath,
				WorkDir:    "/tmp/buildctl-work",
				LogDir:     "./logs",
			})
			if err != nil {
				return fmt.Errorf("failed to create executor: %w", err)
			}

			// If building specific build ID
			if buildID > 0 {
				builds, err := qm.List("", 0)
				if err != nil {
					return fmt.Errorf("failed to list builds: %w", err)
				}

				var build *models.Build
				for _, b := range builds {
					if b.ID == buildID {
						build = &b
						break
					}
				}

				if build == nil {
					return fmt.Errorf("build ID %d not found", buildID)
				}

				return exec.ExecuteBuild(build)
			}

			// Run workers
			fmt.Printf("Starting %d workers for %s\n", workers, arch)

			stopChan := make(chan struct{})
			defer close(stopChan)

			// Start workers
			for i := 0; i < workers; i++ {
				go exec.RunWorker(arch, stopChan)
			}

			// Wait for max duration or until no more builds
			if maxDuration > 0 {
				fmt.Printf("Will run for maximum %d minutes\n", maxDuration)
				time.Sleep(time.Duration(maxDuration) * time.Minute)
			} else {
				// Run until no more builds
				for {
					stats, err := qm.GetStats()
					if err != nil {
						return fmt.Errorf("failed to get stats: %w", err)
					}

					if stats.Queued == 0 && stats.Building == 0 {
						fmt.Println("No more builds in queue")
						break
					}

					time.Sleep(10 * time.Second)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "Path to soarpkgs repository (required)")
	cmd.Flags().IntVar(&workers, "workers", 1, "Number of parallel workers")
	cmd.Flags().StringVar(&arch, "arch", "x86_64-Linux", "Architecture to build")
	cmd.Flags().StringVar(&sbuildPath, "sbuild", "sbuild", "Path to sbuild binary")
	cmd.Flags().IntVar(&maxDuration, "max-duration", 0, "Maximum duration in minutes (0 = unlimited)")
	cmd.Flags().Int64Var(&buildID, "id", 0, "Build specific build ID")
	cmd.MarkFlagRequired("repo")

	return cmd
}

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List builds by status",
		RunE: func(cmd *cobra.Command, args []string) error {
			qm, err := queue.New(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer qm.Close()

			status := models.BuildStatus("")
			if cmd.Flags().Changed("status") {
				statusStr, _ := cmd.Flags().GetString("status")
				status = models.BuildStatus(statusStr)
			}

			builds, err := qm.List(status, limit)
			if err != nil {
				return fmt.Errorf("failed to list builds: %w", err)
			}

			if len(builds) == 0 {
				fmt.Println("No builds found")
				return nil
			}

			fmt.Printf("Found %d builds:\n", len(builds))
			fmt.Println()

			for _, build := range builds {
				fmt.Printf("ID: %d | %s | %s [%s] | Created: %s\n",
					build.ID,
					build.Status,
					build.PkgName,
					build.Arch,
					build.CreatedAt.Format("2006-01-02 15:04:05"),
				)
				if build.ErrorMessage != "" {
					fmt.Printf("  Error: %s\n", build.ErrorMessage)
				}
			}

			return nil
		},
	}

	cmd.Flags().String("status", "", "Filter by status (queued, building, succeeded, failed, cancelled)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of builds to list")

	return cmd
}
