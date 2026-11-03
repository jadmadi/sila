package store

import (
	"uuid"
)

// NewUUID returns a new random UUID string generated via the native Go standard library uuid package.
func NewUUID() string {
	return uuid.New().String()
}

// NewV7UUID returns a new time-ordered UUIDv7 string generated via the Go standard library uuid package.
func NewV7UUID() string {
	return uuid.NewV7().String()
}

// ParseUUID parses a UUID string into a native uuid.UUID.
func ParseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// IsValidUUID reports whether s is a valid RFC 9562 UUID.
func IsValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// NewSessionID returns a unique random session identifier formatted as a UUID string.
func NewSessionID() string {
	return uuid.New().String()
}
