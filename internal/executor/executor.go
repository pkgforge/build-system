package executor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkgforge/build-system/internal/queue"
	"github.com/pkgforge/build-system/pkg/models"
)

// Executor handles build execution
type Executor struct {
	qm         *queue.Manager
	sbuildPath string
	repoPath   string
	workDir    string
	logDir     string
}

// Config for executor
type Config struct {
	SbuildPath string
	RepoPath   string
	WorkDir    string
	LogDir     string
}

// New creates a new executor
func New(qm *queue.Manager, config Config) (*Executor, error) {
	// Set defaults
	if config.SbuildPath == "" {
		config.SbuildPath = "sbuild"
	}
	if config.WorkDir == "" {
		config.WorkDir = "/tmp/buildctl-work"
	}
	if config.LogDir == "" {
		config.LogDir = "./logs"
	}

	// Validate repo path
	if config.RepoPath == "" {
		return nil, fmt.Errorf("RepoPath is required")
	}

	// Convert repo path to absolute path
	absRepoPath, err := filepath.Abs(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for repo: %w", err)
	}
	config.RepoPath = absRepoPath

	// Create directories
	if err := os.MkdirAll(config.WorkDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work dir: %w", err)
	}
	if err := os.MkdirAll(config.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}

	return &Executor{
		qm:         qm,
		sbuildPath: config.SbuildPath,
		repoPath:   config.RepoPath,
		workDir:    config.WorkDir,
		logDir:     config.LogDir,
	}, nil
}

// ExecuteNext executes the next build from the queue
func (e *Executor) ExecuteNext(arch string) (*models.Build, error) {
	// Get next build
	build, err := e.qm.GetNext(arch)
	if err != nil {
		return nil, fmt.Errorf("failed to get next build: %w", err)
	}

	if build == nil {
		return nil, nil // No builds in queue
	}

	// Execute the build
	if err := e.ExecuteBuild(build); err != nil {
		return build, err
	}

	return build, nil
}

// ExecuteBuild executes a specific build
func (e *Executor) ExecuteBuild(build *models.Build) error {
	fmt.Printf("Building: %s [%s] (ID: %d)\n", build.PkgName, build.Arch, build.ID)

	// Update status to building
	if err := e.qm.UpdateStatus(build.ID, models.StatusBuilding, ""); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	startTime := time.Now()

	// Create log file
	logFile := filepath.Join(e.logDir, fmt.Sprintf("build-%d-%s.log", build.ID, build.PkgName))
	logWriter, err := os.Create(logFile)
	if err != nil {
		e.qm.UpdateStatus(build.ID, models.StatusFailed, fmt.Sprintf("Failed to create log file: %v", err))
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logWriter.Close()

	// Run sbuild
	if err := e.runSbuild(build, logWriter); err != nil {
		duration := int(time.Since(startTime).Seconds())

		// Read error from log
		errorMsg := fmt.Sprintf("Build failed: %v", err)
		if logContent, readErr := os.ReadFile(logFile); readErr == nil {
			// Get last 500 chars of log
			if len(logContent) > 500 {
				errorMsg = string(logContent[len(logContent)-500:])
			} else {
				errorMsg = string(logContent)
			}
		}

		e.qm.UpdateStatus(build.ID, models.StatusFailed, errorMsg)
		fmt.Printf("  ✗ Failed in %s\n", formatDuration(duration))
		return err
	}

	// Mark as succeeded
	duration := int(time.Since(startTime).Seconds())
	if err := e.qm.UpdateStatus(build.ID, models.StatusSucceeded, ""); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	fmt.Printf("  ✓ Succeeded in %s\n", formatDuration(duration))
	return nil
}

// runSbuild executes the sbuild command
func (e *Executor) runSbuild(build *models.Build, logWriter io.Writer) error {
	// Check if sbuild exists
	if _, err := exec.LookPath(e.sbuildPath); err != nil {
		return fmt.Errorf("sbuild not found at %s: %w", e.sbuildPath, err)
	}

	// Construct full path to recipe file
	recipePath := filepath.Join(e.repoPath, build.RecipePath)

	// Verify recipe file exists
	if _, err := os.Stat(recipePath); err != nil {
		return fmt.Errorf("recipe file not found: %s: %w", recipePath, err)
	}

	// Prepare sbuild command
	// sbuild typically takes: sbuild <recipe.yaml>
	args := []string{recipePath}

	cmd := exec.Command(e.sbuildPath, args...)
	cmd.Dir = e.workDir

	// Set environment variables
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TARGET_ARCH=%s", build.Arch),
		fmt.Sprintf("PKG_NAME=%s", build.PkgName),
		fmt.Sprintf("BUILD_ID=%d", build.ID),
	)

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start sbuild: %w", err)
	}

	// Create multi-writer (log file + console)
	multiWriter := io.MultiWriter(logWriter, os.Stdout)

	// Stream output
	go streamOutput(stdout, multiWriter, "  │ ")
	go streamOutput(stderr, multiWriter, "  │ ")

	// Wait for command to finish
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("sbuild failed: %w", err)
	}

	return nil
}

// streamOutput streams command output with prefix
func streamOutput(reader io.Reader, writer io.Writer, prefix string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fmt.Fprintf(writer, "%s%s\n", prefix, scanner.Text())
	}
}

// formatDuration formats duration in human-readable format
func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	} else if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	} else {
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	}
}

// RunWorker runs a worker that continuously processes builds
func (e *Executor) RunWorker(arch string, stopChan <-chan struct{}) {
	fmt.Printf("Worker started for %s\n", arch)

	for {
		select {
		case <-stopChan:
			fmt.Printf("Worker stopped for %s\n", arch)
			return
		default:
			build, err := e.ExecuteNext(arch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error executing build: %v\n", err)
				time.Sleep(5 * time.Second)
				continue
			}

			if build == nil {
				// No builds in queue, wait a bit
				time.Sleep(10 * time.Second)
				continue
			}
		}
	}
}

// GetSbuildVersion returns the version of sbuild
func GetSbuildVersion(sbuildPath string) (string, error) {
	if sbuildPath == "" {
		sbuildPath = "sbuild"
	}

	cmd := exec.Command(sbuildPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get sbuild version: %w", err)
	}

	version := strings.TrimSpace(string(output))
	return version, nil
}

// CheckSbuildInstalled checks if sbuild is installed and accessible
func CheckSbuildInstalled(sbuildPath string) error {
	if sbuildPath == "" {
		sbuildPath = "sbuild"
	}

	_, err := exec.LookPath(sbuildPath)
	if err != nil {
		return fmt.Errorf("sbuild not found: %w\n\nPlease install sbuild from: https://github.com/pkgforge/sbuilder", err)
	}

	return nil
}
