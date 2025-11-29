package models

import "time"

// Recipe represents an SBUILD recipe from soarpkgs
type Recipe struct {
	PkgID       string `yaml:"pkg_id" json:"pkg_id"`
	Name        string `yaml:"pkg_name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	Description string `yaml:"description" json:"description"`
	Homepage    string `yaml:"homepage" json:"homepage"`
	SourceURL   string `yaml:"src_url" json:"source_url"`
	BuildScript string `json:"build_script"` // Relative path to .yaml file
	FilePath    string `json:"-"`            // Absolute path to .yaml file
}

// Build represents a build job in the queue
type Build struct {
	ID           int64      `json:"id"`
	PkgName      string     `json:"pkg_name"`
	PkgID        string     `json:"pkg_id"`
	RecipePath   string     `json:"recipe_path"`
	Status       string     `json:"status"` // queued, building, succeeded, failed, cancelled
	Priority     int        `json:"priority"`
	Arch         string     `json:"arch"`
	ForceBuild   bool       `json:"force_build"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DurationSecs *int       `json:"duration_seconds,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	BuildLogURL  string     `json:"build_log_url,omitempty"`
}

// BuildStatus represents possible build states
type BuildStatus string

const (
	StatusQueued    BuildStatus = "queued"
	StatusBuilding  BuildStatus = "building"
	StatusSucceeded BuildStatus = "succeeded"
	StatusFailed    BuildStatus = "failed"
	StatusCancelled BuildStatus = "cancelled"
)

// Statistics represents build queue statistics
type Statistics struct {
	TotalBuilds int     `json:"total_builds"`
	Queued      int     `json:"queued"`
	Building    int     `json:"building"`
	Succeeded   int     `json:"succeeded"`
	Failed      int     `json:"failed"`
	Cancelled   int     `json:"cancelled"`
	AvgDuration float64 `json:"avg_duration_seconds"`
	SuccessRate float64 `json:"success_rate_percent"`
}

// PackageMetadata represents metadata for INDEX.json
type PackageMetadata struct {
	PkgID         string    `json:"pkg_id"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	Description   string    `json:"description"`
	Homepage      string    `json:"homepage"`
	SourceURL     string    `json:"source_url"`
	BuildScript   string    `json:"build_script"`
	Architectures []string  `json:"architectures"`
	DownloadCount int       `json:"download_count"`
	Rank          int       `json:"rank"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Index represents the master INDEX.json structure
type Index struct {
	Version       string            `json:"version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	TotalPackages int               `json:"total_packages"`
	Packages      []PackageMetadata `json:"packages"`
}
