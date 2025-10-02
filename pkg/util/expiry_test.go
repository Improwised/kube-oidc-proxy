package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func int32Ptr(i int32) *int32 {
	return &i
}

func TestIsExpired(t *testing.T) {
	now := time.Now()
	farPast := now.Add(-60 * time.Minute)

	testCases := []struct {
		name              string
		creationTimestamp time.Time
		durationMinutes   *int32
		expected          bool
	}{
		{
			name:              "Temporary binding, not expired",
			creationTimestamp: now,
			durationMinutes:   int32Ptr(30),
			expected:          false,
		},
		{
			name:              "Temporary binding, expired",
			creationTimestamp: farPast,
			durationMinutes:   int32Ptr(30),
			expected:          true,
		},
		{
			name:              "Permanent binding, nil duration",
			creationTimestamp: farPast,
			durationMinutes:   nil,
			expected:          false,
		},
		{
			name:              "Permanent binding, zero duration",
			creationTimestamp: farPast,
			durationMinutes:   int32Ptr(0),
			expected:          false,
		},
		{
			name:              "Permanent binding, negative duration",
			creationTimestamp: farPast,
			durationMinutes:   int32Ptr(-10),
			expected:          false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := IsExpired(tc.creationTimestamp, tc.durationMinutes)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
