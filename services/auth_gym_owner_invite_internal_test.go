package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/identifier"
)

type stubOwnerUsers struct {
	existing *db.User
	created  []*db.User
}

func (s *stubOwnerUsers) EmailExists(_ context.Context, email string) (bool, error) {
	return s.existing != nil && s.existing.UserEmail == email, nil
}

func (s *stubOwnerUsers) GetByEmailCheck(_ context.Context, email string) (*db.User, *models.ApiError) {
	if s.existing != nil && s.existing.UserEmail == email {
		return s.existing, nil
	}
	return nil, nil
}

func (s *stubOwnerUsers) CreateUser(_ context.Context, u *db.User) (*db.UserResponse, *models.ApiError) {
	s.created = append(s.created, u)
	return &db.UserResponse{ExternalID: u.ExternalID, Email: u.UserEmail, Role: u.UserRole, Status: string(u.UserStatus), VerificationStatus: u.UserVerificationStatus}, nil
}

type stubOwnerIDs struct{}

func (stubOwnerIDs) GenerateId(context.Context, string) (identifier.Entity, *models.ApiError) {
	return identifier.Entity{Code: "owner-1"}, nil
}

type stubPwChecker struct{}

func (stubPwChecker) HashPassword(p string) (string, *models.ApiError) { return "hashed:" + p, nil }
func (stubPwChecker) VerifyPassword(string, string) *models.ApiError   { return nil }

// stubRedeemer accepts one token for one email; reuse after MarkAccepted gives the generic 404.
type stubRedeemer struct {
	token, email string
	id           primitive.ObjectID
	accepted     []string
}

func (r *stubRedeemer) Redeem(_ context.Context, token, email string) (*db.GymOwnerInvite, *models.ApiError) {
	if token != r.token || len(r.accepted) > 0 {
		return nil, inviteNotFound()
	}
	if email != r.email {
		return nil, models.NewApiError(http.StatusForbidden, models.ErrInviteEmailMismatch)
	}
	return &db.GymOwnerInvite{ID: r.id, Email: r.email, Firstname: "Invited", Lastname: "Owner"}, nil
}

func (r *stubRedeemer) MarkAccepted(_ context.Context, _ string, userID string) *models.ApiError {
	r.accepted = append(r.accepted, userID)
	return nil
}

func newInvitedAuth(users *stubOwnerUsers, red *stubRedeemer, p map[oauth2.ProviderType]oauth2.Provider) *AuthServiceImpl {
	// payment, invitation and notification clients stay nil: touching any of them would panic the test.
	s := &AuthServiceImpl{providers: p, passwordChecker: stubPwChecker{}, ownerUsers: users, ownerIDs: stubOwnerIDs{}}
	return s.WithGymOwnerInvites(red)
}

func TestRegisterInvitedGymOwner(t *testing.T) {
	ctx := context.Background()
	newReq := func(email string) *api.CreateUserRequest {
		return &api.CreateUserRequest{FirstName: "Ada", LastName: "Lovelace", Email: email, Password: "Str0ng!Pass", GymOwnerInviteToken: "tok"}
	}

	t.Run("creates an Active verified gym_owner", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		res, apiErr := newInvitedAuth(users, red, nil).Register(ctx, newReq("o@b.co"))
		require.Nil(t, apiErr)
		require.Len(t, users.created, 1)
		u := users.created[0]
		assert.Equal(t, db.RoleGymOwner, u.UserRole)
		assert.Equal(t, db.Active, u.UserStatus)
		assert.True(t, u.UserVerificationStatus)
		assert.Equal(t, "hashed:Str0ng!Pass", u.UserPassword)
		assert.Nil(t, u.UserConfirmCode, "no confirmation code")
		assert.Equal(t, []string{"owner-1"}, red.accepted)
		assert.Equal(t, string(db.Active), res.User.Status)
		assert.Nil(t, res.User.Token)
	})

	t.Run("reused token is 404", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		s := newInvitedAuth(users, red, nil)
		_, apiErr := s.Register(ctx, newReq("o@b.co"))
		require.Nil(t, apiErr)
		_, apiErr = s.Register(ctx, newReq("o@b.co"))
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.Len(t, users.created, 1)
	})

	t.Run("wrong email is 403", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		_, apiErr := newInvitedAuth(users, red, nil).Register(ctx, newReq("x@b.co"))
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.Empty(t, users.created)
	})

	t.Run("existing email is 409", func(t *testing.T) {
		users := &stubOwnerUsers{existing: &db.User{UserEmail: "o@b.co", UserRole: "client"}}
		red := &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		_, apiErr := newInvitedAuth(users, red, nil).Register(ctx, newReq("o@b.co"))
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Empty(t, users.created)
		assert.Empty(t, red.accepted)
	})

	t.Run("weak password is 400", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		req := newReq("o@b.co")
		req.Password = "weak"
		_, apiErr := newInvitedAuth(users, red, nil).Register(ctx, req)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})
}

func TestHandleOAuthLoginRegistrationLinkRole(t *testing.T) {
	ctx := context.Background()
	providers := map[oauth2.ProviderType]oauth2.Provider{"google": &fakeProvider{claims: &oauth2.TokenClaims{
		Email: "o@b.co", EmailVerified: true, Subject: "sub-1", GivenName: "Gia", FamilyName: "Owner",
	}}}
	req := db.GoogleLoginRequest{Account: db.GoogleAccount{IdToken: "idt"}, Role: "client", RegistrationToken: "reg"}

	for _, role := range []string{db.RoleGymOwner, "coach"} {
		t.Run(role+" account is refused", func(t *testing.T) {
			users := &stubOwnerUsers{existing: &db.User{ExternalID: "u9", UserEmail: "o@b.co", UserRole: role, UserStatus: db.Active}}
			_, apiErr := newInvitedAuth(users, &stubRedeemer{}, providers).HandleOAuthLogin(ctx, "google", req)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusConflict, apiErr.Code)
			assert.ErrorIs(t, apiErr.Error, models.ErrRegistrationLinkRole)
			assert.Empty(t, users.created)
		})
	}
}

func TestHandleOAuthLoginInvitedGymOwner(t *testing.T) {
	ctx := context.Background()
	providers := func(email string) map[oauth2.ProviderType]oauth2.Provider {
		return map[oauth2.ProviderType]oauth2.Provider{"google": &fakeProvider{claims: &oauth2.TokenClaims{
			Email: email, EmailVerified: true, Subject: "sub-1", GivenName: "Gia", FamilyName: "Owner",
		}}}
	}
	req := db.GoogleLoginRequest{Account: db.GoogleAccount{IdToken: "idt"}, Role: "client", GymOwnerInviteToken: "tok"}

	t.Run("new user becomes gym_owner with tokens", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		res, apiErr := newInvitedAuth(users, red, providers("o@b.co")).HandleOAuthLogin(ctx, "google", req)
		require.Nil(t, apiErr)
		require.Len(t, users.created, 1)
		u := users.created[0]
		assert.Equal(t, db.RoleGymOwner, u.UserRole, "client-sent role is ignored")
		assert.Equal(t, db.Active, u.UserStatus)
		require.NotNil(t, u.Token)
		require.NotNil(t, u.UserRefreshToken)
		assert.Equal(t, []string{"owner-1"}, red.accepted)
		require.NotNil(t, res)
		assert.Equal(t, db.RoleGymOwner, res.User.Role)
	})

	t.Run("existing user is 409 and unchanged", func(t *testing.T) {
		existing := &db.User{ExternalID: "u9", UserEmail: "o@b.co", UserRole: "client", UserStatus: db.Active}
		users := &stubOwnerUsers{existing: existing}
		red := &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		_, apiErr := newInvitedAuth(users, red, providers("o@b.co")).HandleOAuthLogin(ctx, "google", req)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, "client", existing.UserRole)
		assert.Empty(t, users.created)
		assert.Empty(t, red.accepted)
	})

	t.Run("verified email differing from the invite is 403", func(t *testing.T) {
		users, red := &stubOwnerUsers{}, &stubRedeemer{token: "tok", email: "o@b.co", id: primitive.NewObjectID()}
		_, apiErr := newInvitedAuth(users, red, providers("other@b.co")).HandleOAuthLogin(ctx, "google", req)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.Empty(t, users.created)
	})
}
