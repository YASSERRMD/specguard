package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore implements the Store interface using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a connection and runs all pending migrations.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

// runMigrations scans the embedded migrations filesystem and applies new files.
func (s *SQLiteStore) runMigrations() error {
	// Create the schema_migrations table if it does not exist
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to check or create migrations table: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var filenames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)

	for _, filename := range filenames {
		version, err := parseMigrationVersion(filename)
		if err != nil {
			return fmt.Errorf("failed to parse migration version from %s: %w", filename, err)
		}

		applied, err := s.isMigrationApplied(version)
		if err != nil {
			return fmt.Errorf("failed to check migration status for version %d: %w", version, err)
		}

		if applied {
			continue
		}

		content, err := migrationsFS.ReadFile(filepath.Join("migrations", filename))
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		if err := s.applyMigration(version, string(content)); err != nil {
			return fmt.Errorf("failed to apply migration version %d (%s): %w", version, filename, err)
		}
	}

	return nil
}

func parseMigrationVersion(filename string) (int, error) {
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid migration filename layout")
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid prefix integer: %w", err)
	}
	return version, nil
}

func (s *SQLiteStore) isMigrationApplied(version int) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SQLiteStore) applyMigration(version int, sqlContent string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute migration queries. SQLite Exec allows multiple statements if the driver configuration allows,
	// but standard statements can also be split or run directly.
	if _, err := tx.Exec(sqlContent); err != nil {
		return fmt.Errorf("execution error: %w", err)
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("failed to register version: %w", err)
	}

	return tx.Commit()
}

// SaveSpec serializes a spec to JSON and inserts or replaces it in the specs table.
func (s *SQLiteStore) SaveSpec(specID string, spec *core.NormalizedSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}

	_, err = s.db.Exec("INSERT OR REPLACE INTO specs (id, spec_data, created_at) VALUES (?, ?, ?)", specID, string(data), time.Now())
	if err != nil {
		return fmt.Errorf("failed to save spec: %w", err)
	}

	return nil
}

// LoadSpec fetches a spec by ID and deserializes it.
func (s *SQLiteStore) LoadSpec(specID string) (*core.NormalizedSpec, error) {
	var specData string
	err := s.db.QueryRow("SELECT spec_data FROM specs WHERE id = ?", specID).Scan(&specData)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("spec not found: %s", specID)
	} else if err != nil {
		return nil, fmt.Errorf("failed to query spec: %w", err)
	}

	var spec core.NormalizedSpec
	if err := json.Unmarshal([]byte(specData), &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal spec: %w", err)
	}

	return &spec, nil
}

// SaveMockConfig serializes a config to JSON and inserts or replaces it in the mock_configs table.
func (s *SQLiteStore) SaveMockConfig(mockID string, config *core.MockConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal mock config: %w", err)
	}

	_, err = s.db.Exec("INSERT OR REPLACE INTO mock_configs (id, config_data, created_at) VALUES (?, ?, ?)", mockID, string(data), time.Now())
	if err != nil {
		return fmt.Errorf("failed to save mock config: %w", err)
	}

	return nil
}

// LoadMockConfig fetches a config by ID and deserializes it.
func (s *SQLiteStore) LoadMockConfig(mockID string) (*core.MockConfig, error) {
	var configData string
	err := s.db.QueryRow("SELECT config_data FROM mock_configs WHERE id = ?", mockID).Scan(&configData)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("mock config not found: %s", mockID)
	} else if err != nil {
		return nil, fmt.Errorf("failed to query mock config: %w", err)
	}

	var config core.MockConfig
	if err := json.Unmarshal([]byte(configData), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mock config: %w", err)
	}

	return &config, nil
}

// SaveContractRun inserts a test run.
func (s *SQLiteStore) SaveContractRun(run *ContractRun) error {
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}

	data, err := json.Marshal(run.DriftReport)
	if err != nil {
		return fmt.Errorf("failed to marshal drift report: %w", err)
	}

	_, err = s.db.Exec(
		"INSERT OR REPLACE INTO contract_runs (id, spec_id, target_url, passed, report_data, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		run.ID, run.SpecID, run.TargetURL, run.Passed, string(data), run.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save contract run: %w", err)
	}

	return nil
}

// GetContractRun fetches a run by ID.
func (s *SQLiteStore) GetContractRun(runID string) (*ContractRun, error) {
	var run ContractRun
	var reportData string
	var createdAtStr string

	err := s.db.QueryRow(
		"SELECT id, spec_id, target_url, passed, report_data, created_at FROM contract_runs WHERE id = ?", runID,
	).Scan(&run.ID, &run.SpecID, &run.TargetURL, &run.Passed, &reportData, &createdAtStr)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("contract run not found: %s", runID)
	} else if err != nil {
		return nil, fmt.Errorf("failed to query contract run: %w", err)
	}

	// SQLite time parsing can be a bit variable depending on timezone formats,
	// parse it manually or let the driver scan direct strings.
	createdAt, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", createdAtStr)
	if err != nil {
		createdAt, err = time.Parse(time.RFC3339, createdAtStr)
	}
	if err != nil {
		// Fallback to SQLite DEFAULT format parsing
		createdAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
	}
	if err != nil {
		// Final fallback: try timezone-less formatted string
		createdAt, err = time.Parse("2006-01-02T15:04:05.999999999Z", createdAtStr)
	}
	if err == nil {
		run.CreatedAt = createdAt
	} else {
		run.CreatedAt = time.Now() // default fallback
	}

	var report core.DriftReport
	if err := json.Unmarshal([]byte(reportData), &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal drift report: %w", err)
	}
	run.DriftReport = &report

	return &run, nil
}

// ListContractRuns queries historical runs for a spec, sorted by created_at DESC.
func (s *SQLiteStore) ListContractRuns(specID string) ([]ContractRun, error) {
	rows, err := s.db.Query(
		"SELECT id, spec_id, target_url, passed, report_data, created_at FROM contract_runs WHERE spec_id = ? ORDER BY created_at DESC",
		specID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query contract runs: %w", err)
	}
	defer rows.Close()

	var runs []ContractRun
	for rows.Next() {
		var run ContractRun
		var reportData string
		var createdAtStr string

		err := rows.Scan(&run.ID, &run.SpecID, &run.TargetURL, &run.Passed, &reportData, &createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan contract run row: %w", err)
		}

		createdAt, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", createdAtStr)
		if err != nil {
			createdAt, err = time.Parse(time.RFC3339, createdAtStr)
		}
		if err != nil {
			createdAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
		}
		if err != nil {
			createdAt, err = time.Parse("2006-01-02T15:04:05.999999999Z", createdAtStr)
		}
		if err == nil {
			run.CreatedAt = createdAt
		}

		var report core.DriftReport
		if err := json.Unmarshal([]byte(reportData), &report); err != nil {
			return nil, fmt.Errorf("failed to unmarshal drift report in list: %w", err)
		}
		run.DriftReport = &report

		runs = append(runs, run)
	}

	return runs, nil
}

// Close closes the underlying SQLite database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
