package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeProfileUsers struct {
	user *db.User
	err  *models.ApiError
}

func (f fakeProfileUsers) GetUserByExternalIdUpdate(_ context.Context, _ string) (*db.User, *models.ApiError) {
	return f.user, f.err
}

type fakeProfileClients struct {
	n       int
	err     error
	withGym *bool
}

func (f *fakeProfileClients) CountFollowedClients(_ context.Context, _ string, withGym bool) (int, error) {
	f.withGym = &withGym
	return f.n, f.err
}

func profileCoach() *db.User {
	city := "Sfax"
	return &db.User{
		ExternalID: "c1", UserFirstname: "Prénom", UserLastname: "Nom", UserRole: "coach",
		UserEmail: "nom@gmail.com", UserPhoneNumber: "+21600000000", UserStatus: db.Active,
		UserProfilePicture: "https://res.cloudinary.com/x/image/upload/p.jpg", UserDescription: "Bio",
		UserAddress: db.Address{City: &city},
		Metadata:    &db.UserMetadata{Specialties: []string{"strength", "running"}, Goal: "get_fit"},
		Personal:    &db.PersonalInfo{BirthDate: "1990-01-01", HeightCm: 180},
	}
}

func TestCoachPublicProfile_Success(t *testing.T) {
	clients := &fakeProfileClients{n: 12}
	svc := services.NewCoachProfileService(fakeProfileUsers{user: profileCoach()}, clients, true)

	p, apiErr := svc.GetPublicProfile(context.Background(), "c1")
	require.Nil(t, apiErr)
	assert.Equal(t, "c1", p.ExternalID)
	assert.Equal(t, "Prénom", p.Firstname)
	assert.Equal(t, "Nom", p.Lastname)
	assert.Equal(t, "coach", p.Role)
	assert.Equal(t, "Bio", p.Bio)
	assert.Equal(t, "Sfax", p.City)
	assert.Equal(t, []string{"strength", "running"}, p.Specialties)
	require.NotNil(t, p.Clients)
	assert.Equal(t, 12, *p.Clients)
	require.NotNil(t, clients.withGym)
	assert.True(t, *clients.withGym, "gym relations flag is passed to the count")
}

func TestCoachPublicProfile_NutritionistAndEmptyFields(t *testing.T) {
	u := &db.User{ExternalID: "n1", UserRole: "nutritionist", UserStatus: db.ComReg2}
	p, apiErr := services.NewCoachProfileService(fakeProfileUsers{user: u}, &fakeProfileClients{}, false).GetPublicProfile(context.Background(), "n1")
	require.Nil(t, apiErr)
	assert.Equal(t, []string{}, p.Specialties, "always an array")
	assert.Empty(t, p.City)
	require.NotNil(t, p.Clients)
	assert.Equal(t, 0, *p.Clients)
}

func TestCoachPublicProfile_NotFound(t *testing.T) {
	cases := map[string]fakeProfileUsers{
		"unknown user": {err: &models.ApiError{Code: http.StatusNotFound, Error: models.ErrUserNotFound}},
		"client":       {user: &db.User{ExternalID: "u1", UserRole: "client", UserStatus: db.Active}},
		"admin":        {user: &db.User{ExternalID: "u1", UserRole: "admin", UserStatus: db.Active}},
		"blocked":      {user: &db.User{ExternalID: "u1", UserRole: "coach", UserStatus: db.Blocked}},
		"deactivated":  {user: &db.User{ExternalID: "u1", UserRole: "coach", UserStatus: db.Deactivated}},
		"unconfirmed":  {user: &db.User{ExternalID: "u1", UserRole: "coach", UserStatus: db.ToConfirm}},
	}
	for name, users := range cases {
		clients := &fakeProfileClients{}
		_, apiErr := services.NewCoachProfileService(users, clients, false).GetPublicProfile(context.Background(), "u1")
		require.NotNil(t, apiErr, name)
		assert.Equal(t, http.StatusNotFound, apiErr.Code, name)
		assert.ErrorIs(t, apiErr.Error, models.ErrCoachNotFound, name)
		assert.Nil(t, clients.withGym, name+": no count for a non-coach")
	}

	_, apiErr := services.NewCoachProfileService(fakeProfileUsers{user: profileCoach()}, &fakeProfileClients{}, false).GetPublicProfile(context.Background(), "")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestCoachPublicProfile_LookupErrorPropagated(t *testing.T) {
	users := fakeProfileUsers{err: &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}}
	_, apiErr := services.NewCoachProfileService(users, &fakeProfileClients{}, false).GetPublicProfile(context.Background(), "c1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}

func TestCoachPublicProfile_CountFailureLeavesClientsOut(t *testing.T) {
	clients := &fakeProfileClients{err: errors.New("mongo down")}
	p, apiErr := services.NewCoachProfileService(fakeProfileUsers{user: profileCoach()}, clients, false).GetPublicProfile(context.Background(), "c1")
	require.Nil(t, apiErr)
	assert.Nil(t, p.Clients)
}
