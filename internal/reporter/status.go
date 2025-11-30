package reporter

import (
	"fmt"
	"strings"
	"time"

	"github.com/pkgforge/build-system/internal/queue"
	"github.com/pkgforge/build-system/pkg/models"
)

// Reporter handles status reporting
type Reporter struct {
	qm *queue.Manager
}

// New creates a new reporter
func New(qm *queue.Manager) *Reporter {
	return &Reporter{qm: qm}
}

// PrintStatus prints the current build queue status
func (r *Reporter) PrintStatus() error {
	stats, err := r.qm.GetStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	fmt.Println("Build Queue Status")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Queued:    %d builds\n", stats.Queued)
	fmt.Printf("Building:  %d builds\n", stats.Building)
	fmt.Printf("Succeeded: %d builds\n", stats.Succeeded)
	fmt.Printf("Failed:    %d builds\n", stats.Failed)
	fmt.Printf("Cancelled: %d builds\n", stats.Cancelled)
	fmt.Printf("Total:     %d builds\n", stats.TotalBuilds)
	fmt.Println()

	// Print recent builds
	recent, err := r.qm.List("", 10)
	if err != nil {
		return fmt.Errorf("failed to list recent builds: %w", err)
	}

	if len(recent) > 0 {
		fmt.Println("Recent Builds:")
		fmt.Println(strings.Repeat("-", 50))
		for _, build := range recent {
			r.printBuild(&build)
		}
	}

	return nil
}

// PrintStats prints detailed statistics
func (r *Reporter) PrintStats() error {
	stats, err := r.qm.GetStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	fmt.Println("Build Statistics")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Total Builds:     %d\n", stats.TotalBuilds)
	fmt.Printf("Success Rate:     %.2f%%\n", stats.SuccessRate)
	fmt.Printf("Average Duration: %.2f seconds\n", stats.AvgDuration)
	fmt.Println()

	fmt.Println("Status Breakdown:")
	fmt.Printf("  Queued:    %d (%.1f%%)\n", stats.Queued, percent(stats.Queued, stats.TotalBuilds))
	fmt.Printf("  Building:  %d (%.1f%%)\n", stats.Building, percent(stats.Building, stats.TotalBuilds))
	fmt.Printf("  Succeeded: %d (%.1f%%)\n", stats.Succeeded, percent(stats.Succeeded, stats.TotalBuilds))
	fmt.Printf("  Failed:    %d (%.1f%%)\n", stats.Failed, percent(stats.Failed, stats.TotalBuilds))
	fmt.Printf("  Cancelled: %d (%.1f%%)\n", stats.Cancelled, percent(stats.Cancelled, stats.TotalBuilds))

	return nil
}

// PrintPackageStatus prints status for a specific package
func (r *Reporter) PrintPackageStatus(pkgName string) error {
	builds, err := r.qm.GetByPackage(pkgName)
	if err != nil {
		return fmt.Errorf("failed to get builds for package: %w", err)
	}

	if len(builds) == 0 {
		fmt.Printf("No builds found for package: %s\n", pkgName)
		return nil
	}

	fmt.Printf("Build History for: %s\n", pkgName)
	fmt.Println(strings.Repeat("=", 50))

	for _, build := range builds {
		r.printBuild(&build)
	}

	return nil
}

// printBuild prints a single build
func (r *Reporter) printBuild(build *models.Build) {
	statusIcon := statusIcon(build.Status)
	duration := "N/A"

	if build.DurationSecs != nil {
		duration = formatDuration(*build.DurationSecs)
	} else if build.Status == string(models.StatusBuilding) && build.StartedAt != nil {
		elapsed := int(time.Since(*build.StartedAt).Seconds())
		duration = formatDuration(elapsed) + " (in progress)"
	}

	fmt.Printf("%s %-20s [%-14s] %8s  %s\n",
		statusIcon,
		truncate(build.PkgName, 20),
		build.Arch,
		duration,
		build.CreatedAt.Format("2006-01-02 15:04"),
	)

	if build.ErrorMessage != "" {
		fmt.Printf("    Error: %s\n", truncate(build.ErrorMessage, 60))
	}
}

// ExportMarkdown exports status as markdown
func (r *Reporter) ExportMarkdown() (string, error) {
	stats, err := r.qm.GetStats()
	if err != nil {
		return "", fmt.Errorf("failed to get stats: %w", err)
	}

	var sb strings.Builder

	sb.WriteString("# Build Queue Status\n\n")
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Status | Count | Percentage |\n")
	sb.WriteString("|--------|-------|------------|\n")
	sb.WriteString(fmt.Sprintf("| Queued | %d | %.1f%% |\n", stats.Queued, percent(stats.Queued, stats.TotalBuilds)))
	sb.WriteString(fmt.Sprintf("| Building | %d | %.1f%% |\n", stats.Building, percent(stats.Building, stats.TotalBuilds)))
	sb.WriteString(fmt.Sprintf("| Succeeded | %d | %.1f%% |\n", stats.Succeeded, percent(stats.Succeeded, stats.TotalBuilds)))
	sb.WriteString(fmt.Sprintf("| Failed | %d | %.1f%% |\n", stats.Failed, percent(stats.Failed, stats.TotalBuilds)))
	sb.WriteString(fmt.Sprintf("| Cancelled | %d | %.1f%% |\n", stats.Cancelled, percent(stats.Cancelled, stats.TotalBuilds)))
	sb.WriteString(fmt.Sprintf("| **Total** | **%d** | **100.0%%** |\n\n", stats.TotalBuilds))

	sb.WriteString("## Statistics\n\n")
	sb.WriteString(fmt.Sprintf("- **Success Rate:** %.2f%%\n", stats.SuccessRate))
	sb.WriteString(fmt.Sprintf("- **Average Duration:** %.2f seconds\n\n", stats.AvgDuration))

	// Recent builds
	recent, err := r.qm.List("", 20)
	if err != nil {
		return "", fmt.Errorf("failed to list recent builds: %w", err)
	}

	if len(recent) > 0 {
		sb.WriteString("## Recent Builds\n\n")
		sb.WriteString("| Status | Package | Arch | Duration | Created At |\n")
		sb.WriteString("|--------|---------|------|----------|------------|\n")

		for _, build := range recent {
			duration := "N/A"
			if build.DurationSecs != nil {
				duration = formatDuration(*build.DurationSecs)
			}

			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				build.Status,
				build.PkgName,
				build.Arch,
				duration,
				build.CreatedAt.Format("2006-01-02 15:04"),
			))
		}
	}

	return sb.String(), nil
}

func statusIcon(status string) string {
	switch status {
	case string(models.StatusSucceeded):
		return "✓"
	case string(models.StatusFailed):
		return "✗"
	case string(models.StatusBuilding):
		return "⏳"
	case string(models.StatusQueued):
		return "⏸"
	case string(models.StatusCancelled):
		return "⊗"
	default:
		return "?"
	}
}

func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	} else if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	} else {
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func percent(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total) * 100
}
