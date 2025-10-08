package util

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CalculateExpirationTime calculates the expiration time for a resource with a given creation timestamp and duration.
// It returns the expiration time and the remaining duration until expiration.
// Permanent resources (durationMinutes is nil or <= 0) will return zero time and zero duration.
func CalculateExpirationTime(creationTimestamp time.Time, durationMinutes *int32) (time.Time, time.Duration) {
	if durationMinutes == nil || *durationMinutes <= 0 {
		return time.Time{}, 0
	}
	expirationTime := creationTimestamp.Add(time.Duration(*durationMinutes) * time.Minute)
	remainingDuration := time.Until(expirationTime)
	return expirationTime, remainingDuration
}

// IsBindingExpired checks if a binding has expired based on either time-based expiration or status conditions.
// It first checks if the binding has an "Expired" condition with status "True".
// If no explicit expiration condition exists, it falls back to time-based calculation.
// Permanent resources (durationMinutes is nil or <= 0) will always return false.
func IsBindingExpired(conditions []metav1.Condition, creationTimestamp time.Time, durationMinutes *int32) bool {
	// First check if there's an explicit expired condition
	for _, c := range conditions {
		if c.Type == "Expired" && c.Status == metav1.ConditionTrue {
			return true
		}
	}

	// Fall back to time-based expiration calculation
	if durationMinutes == nil || *durationMinutes <= 0 {
		return false
	}
	expirationTime := creationTimestamp.Add(time.Duration(*durationMinutes) * time.Minute)
	return time.Now().After(expirationTime)
}
