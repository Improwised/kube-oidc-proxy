package audit

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithCustomAuditLog(t *testing.T) {
	t.Run("without audit webhook client should return original handler", func(t *testing.T) {
		a := &Audit{
			client: nil,
		}

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
		})

		wrapped := a.WithCustomAuditLog(handler)

		// When a.client is nil, WithCustomAuditLog returns the original handler
		assert.ObjectsAreEqual(handler, wrapped)

		// Ensure it still works
		wrapped.ServeHTTP(nil, nil)
		assert.True(t, handlerCalled)
	})
}

func TestSendAuditLog(t *testing.T) {
	t.Run("without audit webhook client should not panic", func(t *testing.T) {
		a := &Audit{
			client: nil,
		}

		// Should not panic
		assert.NotPanics(t, func() {
			a.SendAuditLog(Log{})
		})
	})
}
