package disk

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
)

// SQLiteStore implements SessionStorer using SQLite
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-based storage instance
func NewSQLiteStore() (interfaces.SessionStorer, error) {
	// Check for custom database path (useful for testing)
	dbPath := os.Getenv("GROVE_HOOKS_DB_PATH")
	if dbPath == "" {
		// Use default path
		dataDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "grove-hooks")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %w", err)
		}
		dbPath = filepath.Join(dataDir, "state.db")
	}
	
	return NewSQLiteStoreWithPath(dbPath)
}

// NewSQLiteStoreWithPath creates a new SQLite-based storage instance with a custom path
func NewSQLiteStoreWithPath(dbPath string) (interfaces.SessionStorer, error) {
	// Create directory if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return store, nil
}

// migrate creates the necessary tables if they don't exist
func (s *SQLiteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		pid INTEGER,
		repo TEXT,
		branch TEXT,
		tmux_key TEXT,
		working_directory TEXT,
		user TEXT,
		status TEXT NOT NULL,
		started_at DATETIME NOT NULL,
		ended_at DATETIME,
		last_activity DATETIME,
		is_test BOOLEAN DEFAULT 0,
		is_deleted BOOLEAN DEFAULT 0,
		tool_stats TEXT,
		session_summary TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS tool_executions (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		command TEXT,
		args TEXT,
		status TEXT NOT NULL,
		duration_ms INTEGER,
		error_message TEXT,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		type TEXT NOT NULL,
		title TEXT,
		message TEXT,
		level TEXT,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
	CREATE INDEX IF NOT EXISTS idx_sessions_started_at ON sessions(started_at);
	CREATE INDEX IF NOT EXISTS idx_tool_executions_session_id ON tool_executions(session_id);
	CREATE INDEX IF NOT EXISTS idx_notifications_session_id ON notifications(session_id);
	CREATE INDEX IF NOT EXISTS idx_events_session_id ON events(session_id);
	`

	_, err := s.db.Exec(schema)
	return err
}

// EnsureSessionExists creates or updates a session
func (s *SQLiteStore) EnsureSessionExists(session *models.Session) error {
	// Marshal complex fields
	toolStatsJSON := "{}"
	if session.ToolStats != nil {
		data, err := json.Marshal(session.ToolStats)
		if err != nil {
			return fmt.Errorf("failed to marshal tool stats: %w", err)
		}
		toolStatsJSON = string(data)
	}

	sessionSummaryJSON := "{}"
	if session.SessionSummary != nil {
		data, err := json.Marshal(session.SessionSummary)
		if err != nil {
			return fmt.Errorf("failed to marshal session summary: %w", err)
		}
		sessionSummaryJSON = string(data)
	}

	query := `
	INSERT INTO sessions (
		id, pid, repo, branch, tmux_key, working_directory, user,
		status, started_at, last_activity, is_test, tool_stats, session_summary
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		status = excluded.status,
		last_activity = excluded.last_activity,
		tool_stats = excluded.tool_stats,
		session_summary = excluded.session_summary,
		updated_at = CURRENT_TIMESTAMP
	`

	_, err := s.db.Exec(query,
		session.ID, session.PID, session.Repo, session.Branch, session.TmuxKey,
		session.WorkingDirectory, session.User, session.Status, session.StartedAt,
		session.LastActivity, session.IsTest, toolStatsJSON, sessionSummaryJSON,
	)

	return err
}

// GetSession retrieves a session by ID
func (s *SQLiteStore) GetSession(sessionID string) (*models.Session, error) {
	query := `
	SELECT id, pid, repo, branch, tmux_key, working_directory, user,
		status, started_at, ended_at, last_activity, is_test,
		tool_stats, session_summary
	FROM sessions WHERE id = ?
	`

	var session models.Session
	var endedAt sql.NullTime
	var toolStatsJSON, sessionSummaryJSON string

	err := s.db.QueryRow(query, sessionID).Scan(
		&session.ID, &session.PID, &session.Repo, &session.Branch,
		&session.TmuxKey, &session.WorkingDirectory, &session.User,
		&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
		&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		return nil, err
	}

	if endedAt.Valid {
		session.EndedAt = &endedAt.Time
	}

	// Unmarshal complex fields
	if toolStatsJSON != "" && toolStatsJSON != "{}" {
		var toolStats models.ToolStatistics
		if err := json.Unmarshal([]byte(toolStatsJSON), &toolStats); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tool stats: %w", err)
		}
		session.ToolStats = &toolStats
	}

	if sessionSummaryJSON != "" && sessionSummaryJSON != "{}" {
		var summary models.Summary
		if err := json.Unmarshal([]byte(sessionSummaryJSON), &summary); err != nil {
			return nil, fmt.Errorf("failed to unmarshal session summary: %w", err)
		}
		session.SessionSummary = &summary
	}

	return &session, nil
}

// GetAllSessions retrieves all sessions
func (s *SQLiteStore) GetAllSessions() ([]*models.Session, error) {
	query := `
	SELECT id, pid, repo, branch, tmux_key, working_directory, user,
		status, started_at, ended_at, last_activity, is_test,
		tool_stats, session_summary
	FROM sessions
	WHERE is_deleted = 0
	ORDER BY started_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		var session models.Session
		var endedAt sql.NullTime
		var toolStatsJSON, sessionSummaryJSON string

		err := rows.Scan(
			&session.ID, &session.PID, &session.Repo, &session.Branch,
			&session.TmuxKey, &session.WorkingDirectory, &session.User,
			&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
			&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
		)
		if err != nil {
			return nil, err
		}

		if endedAt.Valid {
			session.EndedAt = &endedAt.Time
		}

		// Unmarshal complex fields
		if toolStatsJSON != "" && toolStatsJSON != "{}" {
			var toolStats models.ToolStatistics
			if err := json.Unmarshal([]byte(toolStatsJSON), &toolStats); err != nil {
				// Log error but continue
				log.Printf("Failed to unmarshal tool stats for session %s: %v", session.ID, err)
			} else {
				session.ToolStats = &toolStats
			}
		}

		if sessionSummaryJSON != "" && sessionSummaryJSON != "{}" {
			var summary models.Summary
			if err := json.Unmarshal([]byte(sessionSummaryJSON), &summary); err != nil {
				// Log error but continue
				log.Printf("Failed to unmarshal session summary for session %s: %v", session.ID, err)
			} else {
				session.SessionSummary = &summary
			}
		}

		sessions = append(sessions, &session)
	}

	return sessions, rows.Err()
}

// UpdateSessionStatus updates the status of a session
func (s *SQLiteStore) UpdateSessionStatus(sessionID, status string) error {
	query := `UPDATE sessions SET status = ?, last_activity = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	
	if status == "completed" || status == "failed" || status == "error" {
		query = `UPDATE sessions SET status = ?, ended_at = CURRENT_TIMESTAMP, last_activity = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	}

	_, err := s.db.Exec(query, status, sessionID)
	return err
}

// LogToolUsage logs a new tool execution
func (s *SQLiteStore) LogToolUsage(sessionID string, tool *models.ToolExecution) error {
	paramsJSON, err := json.Marshal(tool.Parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}

	var resultSummaryJSON string
	if tool.ResultSummary != nil {
		data, err := json.Marshal(tool.ResultSummary)
		if err != nil {
			return fmt.Errorf("failed to marshal result summary: %w", err)
		}
		resultSummaryJSON = string(data)
	}

	query := `
	INSERT INTO tool_executions (
		id, session_id, tool_name, command, args, status, started_at, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	// Extract command from parameters if available
	command := ""
	if cmd, ok := tool.Parameters["command"].(string); ok {
		command = cmd
	} else if filePath, ok := tool.Parameters["file_path"].(string); ok {
		command = filePath
	}

	status := "running"
	if tool.Success != nil {
		if *tool.Success {
			status = "completed"
		} else {
			status = "failed"
		}
	}

	_, err = s.db.Exec(query,
		tool.ID, sessionID, tool.ToolName, command,
		string(paramsJSON), status, tool.StartedAt, resultSummaryJSON,
	)

	return err
}

// UpdateToolExecution updates an existing tool execution
func (s *SQLiteStore) UpdateToolExecution(sessionID, toolID string, update *models.ToolExecution) error {
	var resultSummaryJSON string
	if update.ResultSummary != nil {
		data, err := json.Marshal(update.ResultSummary)
		if err != nil {
			return fmt.Errorf("failed to marshal result summary: %w", err)
		}
		resultSummaryJSON = string(data)
	}

	status := "running"
	if update.Success != nil {
		if *update.Success {
			status = "completed"
		} else {
			status = "failed"
		}
	}

	query := `
	UPDATE tool_executions 
	SET status = ?, duration_ms = ?, error_message = ?, completed_at = ?, 
		metadata = ?, updated_at = CURRENT_TIMESTAMP
	WHERE id = ? AND session_id = ?
	`

	_, err := s.db.Exec(query,
		status, update.DurationMs, update.Error,
		update.CompletedAt, resultSummaryJSON, toolID, sessionID,
	)

	return err
}

// GetToolExecution retrieves a tool execution by ID
func (s *SQLiteStore) GetToolExecution(sessionID, toolID string) (*models.ToolExecution, error) {
	query := `
	SELECT id, tool_name, command, args, status, duration_ms, error_message,
		started_at, completed_at, metadata
	FROM tool_executions
	WHERE id = ? AND session_id = ?
	`

	var tool models.ToolExecution
	var paramsJSON, resultSummaryJSON string
	var durationMs sql.NullInt64
	var errorMessage sql.NullString
	var completedAt sql.NullTime
	var status, command string

	err := s.db.QueryRow(query, toolID, sessionID).Scan(
		&tool.ID, &tool.ToolName, &command, &paramsJSON,
		&status, &durationMs, &errorMessage,
		&tool.StartedAt, &completedAt, &resultSummaryJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("tool execution not found")
		}
		return nil, err
	}

	tool.SessionID = sessionID

	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &tool.Parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
		}
	}

	if resultSummaryJSON != "" && resultSummaryJSON != "{}" {
		var summary models.ToolResultSummary
		if err := json.Unmarshal([]byte(resultSummaryJSON), &summary); err != nil {
			return nil, fmt.Errorf("failed to unmarshal result summary: %w", err)
		}
		tool.ResultSummary = &summary
	}

	if durationMs.Valid {
		duration := durationMs.Int64
		tool.DurationMs = &duration
	}

	if errorMessage.Valid {
		tool.Error = errorMessage.String
	}

	if completedAt.Valid {
		tool.CompletedAt = &completedAt.Time
	}

	// Set success based on status
	if status == "completed" {
		success := true
		tool.Success = &success
	} else if status == "failed" {
		success := false
		tool.Success = &success
	}

	return &tool, nil
}

// LogNotification logs a notification
func (s *SQLiteStore) LogNotification(sessionID string, notification *models.ClaudeNotification) error {
	query := `
	INSERT INTO notifications (session_id, type, message, level, metadata)
	VALUES (?, ?, ?, ?, ?)
	`

	// Create metadata JSON with any extra info
	metadata := map[string]interface{}{
		"system_notification_sent": notification.SystemNotificationSent,
	}
	metadataJSON, _ := json.Marshal(metadata)

	_, err := s.db.Exec(query,
		sessionID, notification.Type,
		notification.Message, notification.Level, string(metadataJSON),
	)

	return err
}

// LogEvent logs an event
func (s *SQLiteStore) LogEvent(sessionID string, event *models.Event) error {
	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		// If Data is nil or empty, use empty JSON object
		dataJSON = []byte("{}")
	}

	query := `
	INSERT INTO events (session_id, type, name, metadata)
	VALUES (?, ?, ?, ?)
	`

	_, err = s.db.Exec(query, sessionID, event.Type, string(event.Type), string(dataJSON))
	return err
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}