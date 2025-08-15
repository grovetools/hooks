package interfaces

import "github.com/mattsolo1/grove-core/pkg/models"

// SessionStorer defines the interface for session state persistence.
type SessionStorer interface {
	// Session management
	EnsureSessionExists(session *models.Session) error
	GetSession(sessionID string) (*models.Session, error)
	GetAllSessions() ([]*models.Session, error)
	UpdateSessionStatus(sessionID, status string) error
	
	// Tool execution tracking
	LogToolUsage(sessionID string, tool *models.ToolExecution) error
	UpdateToolExecution(sessionID, toolID string, update *models.ToolExecution) error
	GetToolExecution(sessionID, toolID string) (*models.ToolExecution, error)
	
	// Notification tracking
	LogNotification(sessionID string, notification *models.ClaudeNotification) error
	
	// Event tracking
	LogEvent(sessionID string, event *models.Event) error
}