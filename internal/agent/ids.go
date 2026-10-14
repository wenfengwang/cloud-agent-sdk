package agent

import (
	"fmt"

	"github.com/google/uuid"
)

// newID returns a new UUIDv7 string.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("cloudagent: generate id: %w", err)
	}
	return id.String(), nil
}
