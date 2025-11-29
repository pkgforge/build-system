package queue

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/pkgforge/build-system/pkg/models"
)

// Manager handles build queue operations
type Manager struct {
	db *sql.DB
}

// New creates a new queue manager
func New(dbPath string) (*Manager, error) {
	db, err := InitDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Manager{db: db}, nil
}

// Close closes the database connection
func (m *Manager) Close() error {
	return m.db.Close()
}

// Add adds a build to the queue
func (m *Manager) Add(pkgName, pkgID, recipePath, arch string, priority int, forceBuild bool) (int64, error) {
	result, err := m.db.Exec(`
		INSERT INTO builds (pkg_name, pkg_id, recipe_path, status, priority, arch, force_build)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, pkgName, pkgID, recipePath, models.StatusQueued, priority, arch, forceBuild)

	if err != nil {
		return 0, fmt.Errorf("failed to add build: %w", err)
	}

	return result.LastInsertId()
}

// GetNext fetches the next build from the queue (highest priority, oldest first)
func (m *Manager) GetNext(arch string) (*models.Build, error) {
	query := `
		SELECT id, pkg_name, pkg_id, recipe_path, status, priority, arch,
		       force_build, created_at, started_at, completed_at,
		       duration_seconds, error_message, build_log_url
		FROM builds
		WHERE status = ? AND arch = ?
		ORDER BY priority DESC, created_at ASC
		LIMIT 1
	`

	var build models.Build
	var startedAt, completedAt sql.NullTime
	var duration sql.NullInt64
	var errorMessage, buildLogURL sql.NullString

	err := m.db.QueryRow(query, models.StatusQueued, arch).Scan(
		&build.ID, &build.PkgName, &build.PkgID, &build.RecipePath,
		&build.Status, &build.Priority, &build.Arch, &build.ForceBuild,
		&build.CreatedAt, &startedAt, &completedAt, &duration,
		&errorMessage, &buildLogURL,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get next build: %w", err)
	}

	if startedAt.Valid {
		build.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		build.CompletedAt = &completedAt.Time
	}
	if duration.Valid {
		durationInt := int(duration.Int64)
		build.DurationSecs = &durationInt
	}
	if errorMessage.Valid {
		build.ErrorMessage = errorMessage.String
	}
	if buildLogURL.Valid {
		build.BuildLogURL = buildLogURL.String
	}

	return &build, nil
}

// UpdateStatus updates the status of a build
func (m *Manager) UpdateStatus(buildID int64, status models.BuildStatus, errorMsg string) error {
	now := time.Now()

	query := `UPDATE builds SET status = ?, error_message = ?`
	args := []interface{}{status, errorMsg}

	if status == models.StatusBuilding {
		query += `, started_at = ?`
		args = append(args, now)
	} else if status == models.StatusSucceeded || status == models.StatusFailed || status == models.StatusCancelled {
		query += `, completed_at = ?, duration_seconds = (
			SELECT CAST((julianday(?) - julianday(started_at)) * 86400 AS INTEGER)
			FROM builds WHERE id = ?
		)`
		args = append(args, now, now, buildID)
	}

	query += ` WHERE id = ?`
	args = append(args, buildID)

	_, err := m.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

// List returns builds matching the given filter
func (m *Manager) List(status models.BuildStatus, limit int) ([]models.Build, error) {
	query := `
		SELECT id, pkg_name, pkg_id, recipe_path, status, priority, arch,
		       force_build, created_at, started_at, completed_at,
		       duration_seconds, error_message, build_log_url
		FROM builds
	`
	args := []interface{}{}

	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY created_at DESC`

	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}
	defer rows.Close()

	var builds []models.Build
	for rows.Next() {
		var build models.Build
		var startedAt, completedAt sql.NullTime
		var duration sql.NullInt64
		var errorMessage, buildLogURL sql.NullString

		err := rows.Scan(
			&build.ID, &build.PkgName, &build.PkgID, &build.RecipePath,
			&build.Status, &build.Priority, &build.Arch, &build.ForceBuild,
			&build.CreatedAt, &startedAt, &completedAt, &duration,
			&errorMessage, &buildLogURL,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan build: %w", err)
		}

		if startedAt.Valid {
			build.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			build.CompletedAt = &completedAt.Time
		}
		if duration.Valid {
			durationInt := int(duration.Int64)
			build.DurationSecs = &durationInt
		}
		if errorMessage.Valid {
			build.ErrorMessage = errorMessage.String
		}
		if buildLogURL.Valid {
			build.BuildLogURL = buildLogURL.String
		}

		builds = append(builds, build)
	}

	return builds, nil
}

// GetStats returns build statistics
func (m *Manager) GetStats() (*models.Statistics, error) {
	stats := &models.Statistics{}

	// Count by status
	err := m.db.QueryRow(`
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as queued,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as building,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as succeeded,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as failed,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as cancelled
		FROM builds
	`, models.StatusQueued, models.StatusBuilding, models.StatusSucceeded,
		models.StatusFailed, models.StatusCancelled).Scan(
		&stats.TotalBuilds, &stats.Queued, &stats.Building,
		&stats.Succeeded, &stats.Failed, &stats.Cancelled,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	// Calculate average duration and success rate
	var avgDuration sql.NullFloat64
	err = m.db.QueryRow(`
		SELECT AVG(duration_seconds)
		FROM builds
		WHERE duration_seconds IS NOT NULL
	`).Scan(&avgDuration)

	if err != nil {
		return nil, fmt.Errorf("failed to get average duration: %w", err)
	}

	if avgDuration.Valid {
		stats.AvgDuration = avgDuration.Float64
	}

	if stats.Succeeded+stats.Failed > 0 {
		stats.SuccessRate = float64(stats.Succeeded) / float64(stats.Succeeded+stats.Failed) * 100
	}

	return stats, nil
}

// Clear removes all builds with the given status
func (m *Manager) Clear(status models.BuildStatus) error {
	query := "DELETE FROM builds"
	args := []interface{}{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}

	_, err := m.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to clear queue: %w", err)
	}

	return nil
}

// Cancel cancels a build
func (m *Manager) Cancel(buildID int64) error {
	return m.UpdateStatus(buildID, models.StatusCancelled, "Cancelled by user")
}

// GetByPackage returns all builds for a specific package
func (m *Manager) GetByPackage(pkgName string) ([]models.Build, error) {
	query := `
		SELECT id, pkg_name, pkg_id, recipe_path, status, priority, arch,
		       force_build, created_at, started_at, completed_at,
		       duration_seconds, error_message, build_log_url
		FROM builds
		WHERE pkg_name = ?
		ORDER BY created_at DESC
	`

	rows, err := m.db.Query(query, pkgName)
	if err != nil {
		return nil, fmt.Errorf("failed to get builds by package: %w", err)
	}
	defer rows.Close()

	var builds []models.Build
	for rows.Next() {
		var build models.Build
		var startedAt, completedAt sql.NullTime
		var duration sql.NullInt64

		err := rows.Scan(
			&build.ID, &build.PkgName, &build.PkgID, &build.RecipePath,
			&build.Status, &build.Priority, &build.Arch, &build.ForceBuild,
			&build.CreatedAt, &startedAt, &completedAt, &duration,
			&build.ErrorMessage, &build.BuildLogURL,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan build: %w", err)
		}

		if startedAt.Valid {
			build.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			build.CompletedAt = &completedAt.Time
		}
		if duration.Valid {
			durationInt := int(duration.Int64)
			build.DurationSecs = &durationInt
		}

		builds = append(builds, build)
	}

	return builds, nil
}

// SaveSyncState saves the sync state for a repository
func (m *Manager) SaveSyncState(repoName, commitHash string, packagesCount int) error {
	_, err := m.db.Exec(`
		INSERT INTO sync_state (repo_name, last_commit_hash, packages_synced)
		VALUES (?, ?, ?)
	`, repoName, commitHash, packagesCount)

	if err != nil {
		return fmt.Errorf("failed to save sync state: %w", err)
	}

	return nil
}

// GetLastSyncState retrieves the last sync state for a repository
func (m *Manager) GetLastSyncState(repoName string) (commitHash string, syncTime time.Time, err error) {
	err = m.db.QueryRow(`
		SELECT last_commit_hash, last_sync_time
		FROM sync_state
		WHERE repo_name = ?
		ORDER BY last_sync_time DESC
		LIMIT 1
	`, repoName).Scan(&commitHash, &syncTime)

	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get sync state: %w", err)
	}

	return commitHash, syncTime, nil
}
