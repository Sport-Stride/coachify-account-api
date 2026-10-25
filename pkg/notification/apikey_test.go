package notification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendMail_APIKeyHeader(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		wantSet   bool
	}{
		{"sent when configured", "k-123", true},
		{"omitted when empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Values("x-api-key")
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)
			c, err := NewNotificationClient(utils.NotificationAPIConfig{URL: srv.URL, APIKey: tc.key})
			require.NoError(t, err)
			require.NoError(t, c.SendMail(context.Background(), userWithCode()))
			if tc.wantSet {
				assert.Equal(t, []string{tc.key}, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}
