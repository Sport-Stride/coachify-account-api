package identifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateId_APIKeyHeader(t *testing.T) {
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
				_, _ = w.Write([]byte(`{"code":"a","label":"user"}`))
			}))
			t.Cleanup(srv.Close)
			c, err := NewIdentifierClient(utils.IdentifierAPIConfig{URL: srv.URL, APIKey: tc.key})
			require.NoError(t, err)
			_, apiErr := c.GenerateId(context.Background(), "user")
			require.Nil(t, apiErr)
			if tc.wantSet {
				assert.Equal(t, []string{tc.key}, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}
