package util

import (
	"time"
)

// IsExpired checks if a resource with a given creation timestamp and optional duration has expired.
// It returns true if the current time is after the calculated expiration time.
// Permanent resources (durationMinutes is nil or <= 0) will always return false.
func IsExpired(creationTimestamp time.Time, durationMinutes *int32) bool {
	if durationMinutes == nil || *durationMinutes <= 0 {
		return false
	}
	expirationTime := creationTimestamp.Add(time.Duration(*durationMinutes) * time.Minute)
	return time.Now().After(expirationTime)
}
