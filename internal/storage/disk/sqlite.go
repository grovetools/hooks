package disk

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
)

// ExtendedSession wraps the grove-core Session with oneshot-specific fields
type ExtendedSession struct {
	models.Session
	Type                string `json:"type" db:"type"`
	PlanName            string `json:"plan_name" db:"plan_name"`
	PlanDirectory       string `json:"plan_directory" db:"plan_directory"`
	JobTitle            string `json:"job_title" db:"job_title"`
	JobFilePath         string `json:"job_file_path" db:"job_file_path"`
	ClaudeSessionID     string `json:"claude_session_id" db:"claude_session_id"`
	ProjectName         string `json:"project_name" db:"project_name"`
	IsWorktree          bool   `json:"is_worktree" db:"is_worktree"`
	IsEcosystem         bool   `json:"is_ecosystem" db:"is_ecosystem"`
	ParentEcosystemPath string `json:"parent_ecosystem_path" db:"parent_ecosystem_path"`
	Provider            string `json:"provider" db:"provider"`
}

// MarshalJSON implements custom JSON marshaling to include extended fields
func (e *ExtendedSession) MarshalJSON() ([]byte, error) {
	// Create a map with all fields
	data := make(map[string]interface{})

	// Marshal the embedded Session to get its fields
	sessionData, err := json.Marshal(e.Session)
	if err != nil {
		return nil, err
	}

	// Unmarshal into the map
	if err := json.Unmarshal(sessionData, &data); err != nil {
		return nil, err
	}

	// Add extended fields
	data["type"] = e.Type
	data["plan_name"] = e.PlanName
	data["plan_directory"] = e.PlanDirectory
	data["job_title"] = e.JobTitle
	data["job_file_path"] = e.JobFilePath
	data["claude_session_id"] = e.ClaudeSessionID
	data["project_name"] = e.ProjectName
	data["is_worktree"] = e.IsWorktree
	data["is_ecosystem"] = e.IsEcosystem
	data["parent_ecosystem_path"] = e.ParentEcosystemPath
	data["provider"] = e.Provider

	return json.Marshal(data)
}

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

	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Add new columns for oneshot jobs, ignoring errors if they already exist
	alterStatements := []string{
		"ALTER TABLE sessions ADD COLUMN type TEXT DEFAULT 'claude_session'",
		"ALTER TABLE sessions ADD COLUMN plan_name TEXT",
		"ALTER TABLE sessions ADD COLUMN plan_directory TEXT",
		"ALTER TABLE sessions ADD COLUMN job_title TEXT",
		"ALTER TABLE sessions ADD COLUMN job_file_path TEXT",
		"ALTER TABLE sessions ADD COLUMN last_error TEXT",
		"ALTER TABLE sessions ADD COLUMN project_name TEXT",
		"ALTER TABLE sessions ADD COLUMN is_worktree BOOLEAN DEFAULT 0",
		"ALTER TABLE sessions ADD COLUMN is_ecosystem BOOLEAN DEFAULT 0",
		"ALTER TABLE sessions ADD COLUMN parent_ecosystem_path TEXT",
		"ALTER TABLE sessions ADD COLUMN worktree_root_path TEXT",
		"ALTER TABLE sessions ADD COLUMN claude_session_id TEXT",
		"ALTER TABLE sessions ADD COLUMN provider TEXT DEFAULT 'claude'",
	}

	// Execute each ALTER statement separately to tolerate existing columns
	for _, stmt := range alterStatements {
		s.db.Exec(stmt) // Ignore error if column already exists
	}

	return nil
}

// EnsureSessionExists creates or updates a session
func (s *SQLiteStore) EnsureSessionExists(session interface{}) error {
	// Extract the base session
	var baseSession *models.Session
	var sessionType string = "claude_session"
	var planName, planDirectory, jobTitle, jobFilePath, claudeSessionID string
	var projectName, parentEcosystemPath string
	var provider string = "claude"
	var isWorktree, isEcosystem bool

	switch v := session.(type) {
	case *models.Session:
		baseSession = v
	case *ExtendedSession:
		baseSession = &v.Session
		sessionType = v.Type
		if sessionType == "" {
			sessionType = "claude_session"
		}
		planName = v.PlanName
		planDirectory = v.PlanDirectory
		jobTitle = v.JobTitle
		jobFilePath = v.JobFilePath
		claudeSessionID = v.ClaudeSessionID
		projectName = v.ProjectName
		isWorktree = v.IsWorktree
		isEcosystem = v.IsEcosystem
		parentEcosystemPath = v.ParentEcosystemPath
		provider = v.Provider
		if provider == "" {
			provider = "claude"
		}
	default:
		return fmt.Errorf("unsupported session type: %T", session)
	}

	// Marshal complex fields
	toolStatsJSON := "{}"
	if baseSession.ToolStats != nil {
		data, err := json.Marshal(baseSession.ToolStats)
		if err != nil {
			return fmt.Errorf("failed to marshal tool stats: %w", err)
		}
		toolStatsJSON = string(data)
	}

	sessionSummaryJSON := "{}"
	if baseSession.SessionSummary != nil {
		data, err := json.Marshal(baseSession.SessionSummary)
		if err != nil {
			return fmt.Errorf("failed to marshal session summary: %w", err)
		}
		sessionSummaryJSON = string(data)
	}

	query := `
	INSERT INTO sessions (
		id, type, pid, repo, branch, tmux_key, working_directory, user,
		status, started_at, last_activity, is_test, tool_stats, session_summary,
		plan_name, plan_directory, job_title, job_file_path, claude_session_id,
		project_name, is_worktree, is_ecosystem, parent_ecosystem_path, provider
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		status = excluded.status,
		last_activity = excluded.last_activity,
		tool_stats = excluded.tool_stats,
		session_summary = excluded.session_summary,
		claude_session_id = excluded.claude_session_id,
		project_name = excluded.project_name,
		is_worktree = excluded.is_worktree,
		is_ecosystem = excluded.is_ecosystem,
		parent_ecosystem_path = excluded.parent_ecosystem_path,
		provider = excluded.provider,
		updated_at = CURRENT_TIMESTAMP,
		started_at = CASE
			WHEN excluded.status = 'running' THEN excluded.started_at
			ELSE sessions.started_at
		END,
		ended_at = CASE
			WHEN excluded.status = 'running' THEN NULL
			ELSE sessions.ended_at
		END,
		last_error = CASE
			WHEN excluded.status = 'running' THEN NULL
			ELSE sessions.last_error
		END
	`

	_, err := s.db.Exec(query,
		baseSession.ID, sessionType, baseSession.PID, baseSession.Repo, baseSession.Branch, baseSession.TmuxKey,
		baseSession.WorkingDirectory, baseSession.User, baseSession.Status, baseSession.StartedAt,
		baseSession.LastActivity, baseSession.IsTest, toolStatsJSON, sessionSummaryJSON,
		planName, planDirectory, jobTitle, jobFilePath, claudeSessionID,
		projectName, isWorktree, isEcosystem, parentEcosystemPath, provider,
	)

	return err
}

// GetSession retrieves a session by ID
func (s *SQLiteStore) GetSession(sessionID string) (interface{}, error) {
	query := `
	SELECT id, COALESCE(type, 'claude_session'), pid, repo, branch, COALESCE(tmux_key, ''), working_directory, user,
		status, started_at, ended_at, last_activity, is_test,
		COALESCE(tool_stats, ''), COALESCE(session_summary, ''),
		COALESCE(plan_name, ''), COALESCE(plan_directory, ''),
		COALESCE(job_title, ''), COALESCE(job_file_path, ''), COALESCE(claude_session_id, ''),
		COALESCE(project_name, ''), COALESCE(is_worktree, 0), COALESCE(is_ecosystem, 0), COALESCE(parent_ecosystem_path, ''),
		COALESCE(provider, 'claude')
	FROM sessions WHERE id = ?
	`

	var session ExtendedSession
	var endedAt sql.NullTime
	var toolStatsJSON, sessionSummaryJSON string

	err := s.db.QueryRow(query, sessionID).Scan(
		&session.ID, &session.Type, &session.PID, &session.Repo, &session.Branch,
		&session.TmuxKey, &session.WorkingDirectory, &session.User,
		&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
		&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
		&session.PlanName, &session.PlanDirectory,
		&session.JobTitle, &session.JobFilePath, &session.ClaudeSessionID,
		&session.ProjectName, &session.IsWorktree, &session.IsEcosystem, &session.ParentEcosystemPath,
		&session.Provider,
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

	// Return the extended session
	return &session, nil
}

// GetAllExtendedSessions retrieves all sessions as ExtendedSession objects
func (s *SQLiteStore) GetAllExtendedSessions() ([]*ExtendedSession, error) {
	query := `
	SELECT id, COALESCE(type, 'claude_session'), pid, repo, branch, COALESCE(tmux_key, ''), working_directory, user,
		status, started_at, ended_at, last_activity, is_test,
		COALESCE(tool_stats, ''), COALESCE(session_summary, ''),
		COALESCE(plan_name, ''), COALESCE(plan_directory, ''),
		COALESCE(job_title, ''), COALESCE(job_file_path, ''), COALESCE(claude_session_id, ''),
		COALESCE(project_name, ''), COALESCE(is_worktree, 0), COALESCE(is_ecosystem, 0), COALESCE(parent_ecosystem_path, ''),
		COALESCE(provider, 'claude')
	FROM sessions
	WHERE is_deleted = 0
	ORDER BY started_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*ExtendedSession
	for rows.Next() {
		var session ExtendedSession
		var endedAt sql.NullTime
		var toolStatsJSON, sessionSummaryJSON string

		err := rows.Scan(
			&session.ID, &session.Type, &session.PID, &session.Repo, &session.Branch,
			&session.TmuxKey, &session.WorkingDirectory, &session.User,
			&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
			&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
			&session.PlanName, &session.PlanDirectory,
			&session.JobTitle, &session.JobFilePath, &session.ClaudeSessionID,
			&session.ProjectName, &session.IsWorktree, &session.IsEcosystem, &session.ParentEcosystemPath,
			&session.Provider,
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

// GetAllSessions retrieves all sessions
func (s *SQLiteStore) GetAllSessions() ([]*models.Session, error) {
	query := `
	SELECT id, COALESCE(type, 'claude_session'), pid, repo, branch, COALESCE(tmux_key, ''), working_directory, user,
		status, started_at, ended_at, last_activity, is_test,
		COALESCE(tool_stats, ''), COALESCE(session_summary, ''),
		COALESCE(plan_name, ''), COALESCE(plan_directory, ''),
		COALESCE(job_title, ''), COALESCE(job_file_path, ''), COALESCE(claude_session_id, ''),
		COALESCE(project_name, ''), COALESCE(is_worktree, 0), COALESCE(is_ecosystem, 0), COALESCE(parent_ecosystem_path, ''),
		COALESCE(provider, 'claude')
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
		var sessionType, claudeSessionID, provider string
		var projectName, parentEcosystemPath string
		var isWorktree, isEcosystem bool

		err := rows.Scan(
			&session.ID, &sessionType, &session.PID, &session.Repo, &session.Branch,
			&session.TmuxKey, &session.WorkingDirectory, &session.User,
			&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
			&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
			&session.PlanName, &session.PlanDirectory,
			&session.JobTitle, &session.JobFilePath, &claudeSessionID,
			&projectName, &isWorktree, &isEcosystem, &parentEcosystemPath,
			&provider,
		)
		if err != nil {
			return nil, err
		}

		session.Type = sessionType
		session.ClaudeSessionID = claudeSessionID
		session.Provider = provider

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

// UpdateSessionStatusWithError updates the status of a session with an optional error message
func (s *SQLiteStore) UpdateSessionStatusWithError(sessionID, status string, errorMsg string) error {
	query := `UPDATE sessions SET status = ?, last_error = ?, last_activity = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`

	if status == "completed" || status == "failed" || status == "error" {
		query = `UPDATE sessions SET status = ?, last_error = ?, ended_at = CURRENT_TIMESTAMP, last_activity = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	}

	_, err := s.db.Exec(query, status, errorMsg, sessionID)
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

// ArchiveSessions archives multiple sessions by setting is_deleted flag
func (s *SQLiteStore) ArchiveSessions(sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}

	// Build placeholders for the query
	placeholders := make([]string, len(sessionIDs))
	args := make([]interface{}, len(sessionIDs))
	for i, id := range sessionIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		UPDATE sessions 
		SET is_deleted = 1, updated_at = CURRENT_TIMESTAMP 
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	_, err := s.db.Exec(query, args...)
	return err
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
