package mapping

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func sp(s string) *string { return &s }

func fullUser() *db.User {
	return &db.User{
		ID: primitive.NewObjectID(), ExternalID: "ext-1",
		UserFirstname: "Ada", UserLastname: "L", UserEmail: "ada@example.com",
		UserPassword: "$2a$10$hashhashhash", UserRole: "coach", UserStatus: db.Active,
		UserRefreshToken: sp("refresh-secret"), Token: sp("access-secret"),
		UserConfirmCode:       &db.UserConfirmCode{Code: "123456"},
		UserResetPasswordCode: &db.UserResetPasswordCode{Code: "654321"},
		IsHeadCoach:           true,
	}
}

func TestCreateToDbUser(t *testing.T) {
	req := &api.CreateUserRequest{FirstName: "A", LastName: "B", Email: "a@b.c", Role: "client", Autologin: true}
	u := CreateToDbUser(req, "hashed", "id-1", db.UserConfirmCode{Code: "111111"})
	assert.Equal(t, "hashed", u.UserPassword)
	assert.Equal(t, "id-1", u.ExternalID)
	assert.Equal(t, "a@b.c", u.UserEmail)
	assert.False(t, u.UserVerificationStatus, "new accounts start unverified")
	require.NotNil(t, u.UserConfirmCode)
	assert.Equal(t, "111111", u.UserConfirmCode.Code)
	assert.False(t, u.UserCreatedAt.IsZero())
}

func TestToApiUser_NilOptionalStructsDoNotPanic(t *testing.T) {
	u := &db.User{ExternalID: "x"} // no confirm/reset codes, no metadata, no tokens
	var got api.ApiUser
	require.NotPanics(t, func() { got = ToApiUser(u) })
	assert.Equal(t, "x", got.ExternalID)
	require.NotNil(t, got.ResetPasswordCode)
	assert.Empty(t, got.ResetPasswordCode.Code)
}

func TestToApiUser_JSONNeverLeaksCredentials(t *testing.T) {
	got := ToApiUser(fullUser())
	b, err := json.Marshal(got)
	require.NoError(t, err)
	s := string(b)
	assert.NotContains(t, s, "hashhash")
	assert.NotContains(t, s, "123456", "confirm code")
	assert.NotContains(t, s, "654321", "reset code")
	assert.Contains(t, s, "ada@example.com")
}

func TestToApiUserResponse_JSONHidesPassword(t *testing.T) {
	resp := ToUserResponse(fullUser())
	got := ToApiUserResponse(&resp)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "hashhash")
	assert.Equal(t, "ext-1", got.ExternalID)
	assert.True(t, got.IsHeadCoach)
}

func TestToPublicUser_NoCredentialFields(t *testing.T) {
	u := fullUser()
	for name, pu := range map[string]api.PublicUser{
		"from user":     ToPublicUser(u),
		"from response": func() api.PublicUser { r := ToUserResponse(u); return ToPublicUserFromResponse(&r) }(),
	} {
		b, err := json.Marshal(pu)
		require.NoError(t, err, name)
		s := string(b)
		for _, secret := range []string{"hashhash", "refresh-secret", "access-secret", "123456", "654321", "password", "token"} {
			assert.NotContains(t, s, secret, name)
		}
		assert.Equal(t, "ada@example.com", pu.Email, name)
		assert.Equal(t, "coach", pu.Role, name)
	}
}

func TestToRefreshToken(t *testing.T) {
	rt := ToRefreshToken(fullUser())
	assert.Equal(t, "ext-1", rt.ExternalID)
	assert.Equal(t, "coach", rt.UserRole)
	assert.Equal(t, "refresh-secret", *rt.RefreshToken)

	assert.Nil(t, ToRefreshToken(&db.User{}).RefreshToken)
}

func TestToDbUserFromOAuth(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	u := ToDbUserFromOAuth(db.OAuthUser{
		ProviderType: "google", ProviderID: "g-1", Email: "a@b.c", FirstName: "A", LastName: "B",
		AccessToken: "encA", RefreshToken: "encR", Expiry: exp,
	}, "ext-9")
	assert.Equal(t, "ext-9", u.ExternalID)
	assert.Equal(t, db.Active, u.UserStatus)
	assert.Empty(t, u.UserPassword, "OAuth accounts have no password")
	p, ok := u.Providers["google"]
	require.True(t, ok)
	assert.Equal(t, "g-1", p.ProviderID)
	assert.Equal(t, "encA", p.AccessToken)
	assert.Equal(t, exp, p.Expiry)
}

func TestToDbUserFromGoogleProfile(t *testing.T) {
	req := db.GoogleLoginRequest{Role: "client"}
	req.Profile.Email = "a@b.c"
	req.Profile.GivenName = "A"
	req.Profile.FamilyName = "B"
	req.Profile.ProviderType = "google"
	req.Profile.Sub = "sub-1"
	req.Account.ExpiresAt = 1700000000
	u := ToDbUserFromGoogleProfile(req, "ext-2")
	assert.Equal(t, "client", u.UserRole)
	assert.Equal(t, db.Active, u.UserStatus)
	assert.Equal(t, "sub-1", u.Providers["google"].ProviderID)
	assert.Equal(t, int64(1700000000), u.Providers["google"].Expiry.Unix())
	require.NotNil(t, u.UserConfirmCode)
}

func TestToConfirmAndResetResponses(t *testing.T) {
	c := ToConfirmResponse(&api.GetUserConfirm{UserEmail: "a@b.c", UserVerificationStatus: true})
	assert.Equal(t, "a@b.c", c.UserEmail)
	assert.True(t, c.UserVerificationStatus)

	r := ToResetPasswor(&api.ResetPasswordResponse{UserEmail: "a@b.c", UserPassword: "h"})
	assert.Equal(t, "a@b.c", r.UserEmail)
}

func TestSearchUserAPIToDB(t *testing.T) {
	addr := &api.Address{City: sp("Tunis")}
	got := SearchUserAPIToDB(api.SearchUser{Firstname: "A", Email: "e", Role: "coach", Address: addr, Page: 2, Size: 10})
	assert.Equal(t, "A", got.UserFirstname)
	assert.Equal(t, "coach", got.UserRole)
	require.NotNil(t, got.UserAddress)
	assert.Equal(t, "Tunis", *got.UserAddress.City)
	assert.Equal(t, 2, got.Page)

	assert.Nil(t, SearchUserAPIToDB(api.SearchUser{}).UserAddress, "nil address stays nil")
}

func TestUpdateUserAPIToDB(t *testing.T) {
	got := UpdateUserAPIToDB(api.UserRequest{UserFirstname: "A", UserRole: "coach", UserStatus: "Active"}, "ext-1")
	assert.Equal(t, "ext-1", got.ExternalID, "identity comes from the caller, not the body")
	assert.Equal(t, "A", got.UserFirstname)
	assert.Equal(t, db.UserStatus("Active"), got.UserStatus)
}

func TestFormattedDateTime(t *testing.T) {
	_, err := time.Parse("02/01/2006 15:04:05", FormattedDateTime())
	assert.NoError(t, err)
}
