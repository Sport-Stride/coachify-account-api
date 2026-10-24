package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xoauth2 "golang.org/x/oauth2"
)

const testKey = "0123456789abcdef0123456789abcdef"

// tokenServer fakes an OAuth2 token endpoint. status != 200 simulates a rejected code.
func tokenServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewProvider(t *testing.T) {
	p, err := NewProvider(ProviderGoogle, "id", "sec", "http://cb", testKey)
	require.NoError(t, err)
	assert.IsType(t, &GoogleProvider{}, p)

	p, err = NewProvider(ProviderFacebook, "id", "sec", "http://cb", testKey)
	require.NoError(t, err)
	assert.IsType(t, &FacebookProvider{}, p)

	p, err = NewProvider("github", "id", "sec", "http://cb", testKey)
	assert.Nil(t, p)
	assert.Error(t, err)
}

func TestGetLoginURL_CarriesStateAndClient(t *testing.T) {
	for name, p := range map[string]Provider{
		"google":   NewGoogleProvider("client-1", "sec", "http://localhost/cb", testKey),
		"facebook": NewFacebookProvider("client-1", "sec", "http://localhost/cb", testKey),
	} {
		t.Run(name, func(t *testing.T) {
			u, err := url.Parse(p.GetLoginURL("csrf-state-xyz"))
			require.NoError(t, err)
			q := u.Query()
			assert.Equal(t, "csrf-state-xyz", q.Get("state"))
			assert.Equal(t, "client-1", q.Get("client_id"))
			assert.Equal(t, "http://localhost/cb", q.Get("redirect_uri"))
			assert.Equal(t, "code", q.Get("response_type"))
			assert.NotContains(t, u.String(), "sec&", "client secret must not be in the login URL")
			assert.NotEqual(t, q.Get("state"), "")
		})
	}
}

func TestExchangeCode(t *testing.T) {
	ok := tokenServer(t, 200, `{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`)
	bad := tokenServer(t, 400, `{"error":"invalid_grant"}`)

	g := NewGoogleProvider("c", "s", "http://cb", testKey)
	g.config.Endpoint = xoauth2.Endpoint{TokenURL: ok.URL}
	tok, apiErr := g.ExchangeCode(context.Background(), "code")
	require.Nil(t, apiErr)
	assert.Equal(t, "at", tok.AccessToken)

	g.config.Endpoint = xoauth2.Endpoint{TokenURL: bad.URL}
	tok, apiErr = g.ExchangeCode(context.Background(), "bad-code")
	assert.Nil(t, tok)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToExchangeCode)
	assert.NotContains(t, apiErr.Error.Error(), "invalid_grant", "provider detail must not leak")

	f := NewFacebookProvider("c", "s", "http://cb", testKey)
	f.config.Endpoint = xoauth2.Endpoint{TokenURL: ok.URL}
	tok, apiErr = f.ExchangeCode(context.Background(), "code")
	require.Nil(t, apiErr)
	assert.Equal(t, "at", tok.AccessToken)

	f.config.Endpoint = xoauth2.Endpoint{TokenURL: bad.URL}
	tok, apiErr = f.ExchangeCode(context.Background(), "bad-code")
	assert.Nil(t, tok)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToExchangeCode)
}

func TestValidateToken_RejectsGarbage(t *testing.T) {
	g := NewGoogleProvider("c", "s", "http://cb", testKey)
	okv, apiErr := g.ValidateToken(context.Background(), "garbage")
	assert.False(t, okv)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Code)

	f := NewFacebookProvider("c", "s", "http://cb", testKey)
	okv, apiErr = f.ValidateToken(context.Background(), "garbage")
	assert.False(t, okv)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
}

func TestGoogleGetUserInfo_Errors(t *testing.T) {
	g := NewGoogleProvider("c", "s", "http://cb", testKey)

	t.Run("missing id_token", func(t *testing.T) {
		u, apiErr := g.GetUserInfo(context.Background(), &xoauth2.Token{AccessToken: "a"})
		assert.Nil(t, u)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrInvalidIDToken)
	})
	t.Run("invalid id_token", func(t *testing.T) {
		tok := (&xoauth2.Token{AccessToken: "a"}).WithExtra(map[string]interface{}{"id_token": "garbage"})
		u, apiErr := g.GetUserInfo(context.Background(), tok)
		assert.Nil(t, u)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	})
}

func TestGoogleRefreshToken(t *testing.T) {
	g := NewGoogleProvider("c", "s", "http://cb", testKey)

	t.Run("success re-encrypts tokens", func(t *testing.T) {
		srv := tokenServer(t, 200, `{"access_token":"new-at","refresh_token":"new-rt","token_type":"Bearer","expires_in":3600}`)
		g.config.Endpoint = xoauth2.Endpoint{TokenURL: srv.URL}
		encRT, err := utils.Encrypt("old-rt", []byte(testKey))
		require.NoError(t, err)

		d, apiErr := g.RefreshToken(context.Background(), encRT)
		require.Nil(t, apiErr)
		assert.NotContains(t, d.AccessToken, "new-at")
		at, err := utils.Decrypt(d.AccessToken, []byte(testKey))
		require.NoError(t, err)
		assert.Equal(t, "new-at", at)
		rt, err := utils.Decrypt(d.RefreshToken, []byte(testKey))
		require.NoError(t, err)
		assert.Equal(t, "new-rt", rt)
		assert.True(t, d.Expiry.After(time.Now()))
	})

	t.Run("undecryptable stored token", func(t *testing.T) {
		d, apiErr := g.RefreshToken(context.Background(), "not-encrypted")
		assert.Nil(t, d)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	})

	t.Run("provider rejects refresh", func(t *testing.T) {
		srv := tokenServer(t, 400, `{"error":"invalid_grant"}`)
		g.config.Endpoint = xoauth2.Endpoint{TokenURL: srv.URL}
		encRT, _ := utils.Encrypt("old-rt", []byte(testKey))
		d, apiErr := g.RefreshToken(context.Background(), encRT)
		assert.Nil(t, d)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	})
}

// rewriteTransport sends every request to the test server regardless of host.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = rt.target.Scheme
	r.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func fbCtx(t *testing.T, status int, body string) context.Context {
	t.Helper()
	srv := tokenServer(t, status, body)
	u, _ := url.Parse(srv.URL)
	return context.WithValue(context.Background(), xoauth2.HTTPClient, &http.Client{Transport: rewriteTransport{u}})
}

func TestFacebookGetUserInfo(t *testing.T) {
	f := NewFacebookProvider("c", "s", "http://cb", testKey)
	tok := &xoauth2.Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}

	t.Run("success", func(t *testing.T) {
		ctx := fbCtx(t, 200, `{"id":"42","email":"a@b.c","first_name":"A","last_name":"B","picture":{"data":{"url":"http://p"}}}`)
		u, apiErr := f.GetUserInfo(ctx, tok)
		require.Nil(t, apiErr)
		assert.Equal(t, "facebook", u.ProviderType)
		assert.Equal(t, "42", u.ProviderID)
		assert.Equal(t, "a@b.c", u.Email)
		assert.Equal(t, "http://p", u.ProfilePicture)
	})
	t.Run("missing email is 401", func(t *testing.T) {
		ctx := fbCtx(t, 200, `{"id":"42"}`)
		u, apiErr := f.GetUserInfo(ctx, tok)
		assert.Nil(t, u)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrEmailNotProvided)
	})
	t.Run("malformed json", func(t *testing.T) {
		ctx := fbCtx(t, 200, `{not json`)
		u, apiErr := f.GetUserInfo(ctx, tok)
		assert.Nil(t, u)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrFailedToUnmarshalResponse)
	})
	t.Run("transport failure", func(t *testing.T) {
		cl := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, assert.AnError
		})}
		ctx := context.WithValue(context.Background(), xoauth2.HTTPClient, cl)
		u, apiErr := f.GetUserInfo(ctx, tok)
		assert.Nil(t, u)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrRetrievingUser)
		assert.False(t, strings.Contains(apiErr.Error.Error(), assert.AnError.Error()))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFacebookRefreshTokenNoop(t *testing.T) {
	f := NewFacebookProvider("c", "s", "http://cb", testKey)
	d, apiErr := f.RefreshToken(context.Background(), "x")
	assert.Nil(t, d)
	assert.Nil(t, apiErr)
}
