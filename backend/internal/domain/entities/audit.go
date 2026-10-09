package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID           uuid.UUID       `json:"id"`
	UserID       *uuid.UUID      `json:"userId,omitempty"`  // subject
	ActorID      *uuid.UUID      `json:"actorId,omitempty"` // who performed the action
	Action       string          `json:"action"`
	ResourceType string          `json:"resourceType"`
	ResourceID   *uuid.UUID      `json:"resourceId,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	IPAddress    string          `json:"ipAddress"`
	UserAgent    string          `json:"userAgent"`
	RequestID    string          `json:"requestId,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
}
