package invitation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvitationClient_APIKeyHeader(t *testing.T) {
	const okBody = `{"success":true,"exists":true,"invitation":{"external_id":"i1"}}`
	for _, tc := range []struct {
		name, key string
		wantSet   bool
	}{
		{"sent when configured", "k-123", true},
		{"omitted when empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hdr http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hdr = r.Header.Clone()
				_, _ = w.Write([]byte(okBody))
			}))
			t.Cleanup(srv.Close)
			c, err := NewInvitationClient(utils.InvitationAPIConfig{URL: srv.URL, APIKey: tc.key})
			require.NoError(t, err)
			ctx := context.Background()

			check := func(name string) {
				if tc.wantSet {
					assert.Equal(t, []string{tc.key}, hdr.Values("x-api-key"), name)
				} else {
					assert.Empty(t, hdr.Values("x-api-key"), name)
				}
			}

			_, _, apiErr := c.CheckInvitationByEmail(ctx, "a@b.c")
			require.Nil(t, apiErr)
			check("check")

			_, apiErr = c.ValidateInvitation(ctx, "i1", "a@b.c")
			require.Nil(t, apiErr)
			check("validate")

			_, apiErr = c.AcceptInvitation(ctx, "i1", "a@b.c", "refresh-tok")
			require.Nil(t, apiErr)
			check("accept")
			assert.Equal(t, "Bearer refresh-tok", hdr.Get("Authorization"))
		})
	}
}
