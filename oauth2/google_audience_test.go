package oauth2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogleAcceptsWebAndMobileAudiences(t *testing.T) {
	p := NewGoogleProvider("web-id", "sec", "http://cb", testKey, "android-id", "", "ios-id", "web-id")

	assert.Equal(t, []string{"web-id", "android-id", "ios-id"}, p.audiences, "blanks and duplicates are dropped, web first")
	for _, aud := range []string{"web-id", "android-id", "ios-id"} {
		assert.True(t, p.acceptsAudience(aud), aud)
	}
	assert.False(t, p.acceptsAudience("other-app"))
	assert.False(t, p.acceptsAudience(""))
}

func TestGoogleWithoutMobileIDsKeepsWebOnly(t *testing.T) {
	p := NewGoogleProvider("web-id", "sec", "http://cb", testKey)

	assert.Equal(t, []string{"web-id"}, p.audiences)
	assert.True(t, p.acceptsAudience("web-id"))
	assert.False(t, p.acceptsAudience("android-id"))
}

func TestNewProviderPassesMobileAudiencesToGoogle(t *testing.T) {
	p, err := NewProvider(ProviderGoogle, "web-id", "sec", "http://cb", testKey, "android-id")
	require.NoError(t, err)
	g, ok := p.(*GoogleProvider)
	require.True(t, ok)
	assert.True(t, g.acceptsAudience("android-id"))

	// Facebook ignores them.
	_, err = NewProvider(ProviderFacebook, "fb-id", "sec", "http://cb", testKey, "android-id")
	require.NoError(t, err)
}

func TestGoogleIssuer(t *testing.T) {
	assert.True(t, googleIssuer("accounts.google.com"))
	assert.True(t, googleIssuer("https://accounts.google.com"))
	assert.False(t, googleIssuer("https://evil.example.com"))
	assert.False(t, googleIssuer(""))
}
