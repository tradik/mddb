package mddb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// statusWarmingUp is the /health status while the server is starting.
const statusWarmingUp = "warming_up"

// Health is the server's /health answer. Ready is true once the server
// answers 2xx; while it starts, Ready is false and Phase, ElapsedSeconds and
// (for phases that can be measured) Progress say how far it has got.
type Health struct {
	Ready          bool               `json:"-"`
	Status         string             `json:"status"` // healthy|warming_up
	Mode           string             `json:"mode,omitempty"`
	Persistence    *PersistenceStatus `json:"persistence,omitempty"`
	Durable        bool               `json:"durable,omitempty"`
	MCPSessions    map[string]int     `json:"mcpSessions,omitempty"`
	Update         *UpdateStatus      `json:"update,omitempty"`
	Phase          string             `json:"phase,omitempty"`
	ElapsedSeconds int                `json:"elapsedSeconds,omitempty"`
	Progress       *StartupProgress   `json:"progress,omitempty"`
}

// StartupProgress is how far a measurable startup phase is. EtaSeconds is -1
// until a rate is known.
type StartupProgress struct {
	BytesDone  int64   `json:"bytesDone"`
	BytesTotal int64   `json:"bytesTotal"`
	Percent    float64 `json:"percent"`
	EtaSeconds int     `json:"etaSeconds"`
}

// PersistenceStatus is what the server can promise about durability.
type PersistenceStatus struct {
	Path       string               `json:"path"`
	Filesystem string               `json:"filesystem,omitempty"`
	Ephemeral  bool                 `json:"ephemeral"`
	Writable   bool                 `json:"writable"`
	FreeBytes  uint64               `json:"freeBytes,omitempty"`
	Warnings   []PersistenceWarning `json:"warnings,omitempty"`
}

// PersistenceWarning is one problem found with the data directory.
type PersistenceWarning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// UpdateStatus is the server's cached release check.
type UpdateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	CheckedAt int64  `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Status reports whether the server is up. A 503 whose body says
// "warming_up" is a starting server, returned as a Health with Ready=false
// and no error; any other status >= 400, including 503 "unhealthy", is an
// *APIError, so starting can be told apart from broken.
func (c *Client) Status(ctx context.Context) (Health, error) {
	status, body, err := c.send(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return Health{}, err
	}
	var h Health
	decodeErr := json.Unmarshal(body, &h)
	switch {
	case status == http.StatusServiceUnavailable && decodeErr == nil && h.Status == statusWarmingUp:
		return h, nil
	case status >= 400:
		return Health{}, &APIError{StatusCode: status, Body: string(body)}
	case decodeErr != nil:
		return Health{}, fmt.Errorf("mddb: decode response: %w", decodeErr)
	}
	h.Ready = true
	return h, nil
}
