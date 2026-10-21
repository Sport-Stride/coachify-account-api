package services_test

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

// Validation runs before any repository call, so nil repositories are safe here.
func TestAdminListUsersValidation(t *testing.T) {
	svc := services.NewAdminService(nil, nil, nil, nil)
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "id" + strconv.Itoa(i)
	}
	for _, tc := range []struct {
		name string
		q    db.AdminUserQuery
		want string
	}{
		{"role", db.AdminUserQuery{Role: "root"}, "invalid role"},
		{"status", db.AdminUserQuery{Status: "Zombie"}, "invalid status"},
		{"sort", db.AdminUserQuery{Sort: "password"}, "invalid sort"},
		{"order", db.AdminUserQuery{Order: "sideways"}, "invalid order"},
		{"ids", db.AdminUserQuery{IDs: tooMany}, "too many ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, apiErr := svc.ListUsers(context.Background(), tc.q)
			assert.Nil(t, res)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.EqualError(t, apiErr.Error, tc.want)
		})
	}
}

// fakeUsers overrides only the methods the write flows use.
type fakeUsers struct {
	services.AdminUserStore
	detail      *db.AdminUserDetail
	updateCalls []string
	listedRole  string
}

func (f *fakeUsers) GetAdminUserDetail(context.Context, string) (*db.AdminUserDetail, error) {
	return f.detail, nil
}

func (f *fakeUsers) UpdateStatusAndRevoke(_ context.Context, id, status string) (bool, error) {
	f.updateCalls = append(f.updateCalls, id+":"+status)
	return true, nil
}

type fakeAudit struct {
	services.AdminAuditStore
	entries   []*db.AdminAuditEntry
	insertErr error
}

func (f *fakeAudit) Insert(_ context.Context, e *db.AdminAuditEntry) error {
	f.entries = append(f.entries, e)
	return f.insertErr
}

type fakeMailer struct {
	confirmEmails []string
	resetEmails   []string
	err           *models.ApiError
	createdOwners []string
	createErr     *models.ApiError
}

func (f *fakeMailer) CreateGymOwnerAccount(_ context.Context, _, _, email string) (string, *models.ApiError) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.createdOwners = append(f.createdOwners, email)
	return "g1", nil
}

func (f *fakeMailer) ResendConfirmEmail(_ context.Context, email string) *models.ApiError {
	f.confirmEmails = append(f.confirmEmails, email)
	return f.err
}

func (f *fakeMailer) InitResetPassword(_ context.Context, r *api.ResetPasswordRequest) *models.ApiError {
	f.resetEmails = append(f.resetEmails, r.Email)
	return f.err
}

func userDetail(role, status string) *db.AdminUserDetail {
	return &db.AdminUserDetail{AdminUserRow: db.AdminUserRow{ExternalID: "u1", Email: "u1@x.io", Role: role, Status: status}}
}

func TestAdminUpdateUserStatus(t *testing.T) {
	long := strings.Repeat("x", 501)
	for _, tc := range []struct {
		name                  string
		actor, status, reason string
		detail                *db.AdminUserDetail
		wantCode              int
		wantMsg               string
	}{
		{"invalid status", "a1", "Inactive", "r", userDetail("client", "Active"), 400, "invalid status"},
		{"unknown status", "a1", "Zombie", "r", userDetail("client", "Active"), 400, "invalid status"},
		{"reason too long", "a1", "Blocked", long, userDetail("client", "Active"), 400, "reason too long"},
		{"reason required", "a1", "Blocked", "  ", userDetail("client", "Active"), 400, "reason is required"},
		{"own status", "u1", "Blocked", "r", userDetail("client", "Active"), 403, "cannot change own status"},
		{"admin target", "a1", "Blocked", "r", userDetail("admin", "Active"), 403, "cannot change status of an admin"},
		{"not found", "a1", "Blocked", "r", nil, 404, "user not found"},
		{"unchanged", "a1", "Suspended", "r", userDetail("client", "Suspended"), 409, "status unchanged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users, audit := &fakeUsers{detail: tc.detail}, &fakeAudit{}
			svc := services.NewAdminService(users, nil, audit, &fakeMailer{})
			row, apiErr := svc.UpdateUserStatus(context.Background(), tc.actor, "u1", tc.status, tc.reason)
			assert.Nil(t, row)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.wantCode, apiErr.Code)
			assert.EqualError(t, apiErr.Error, tc.wantMsg)
			assert.Empty(t, users.updateCalls)
			assert.Empty(t, audit.entries)
		})
	}

	t.Run("active needs no reason; audit before/after", func(t *testing.T) {
		users, audit := &fakeUsers{detail: userDetail("coach", "Blocked")}, &fakeAudit{}
		svc := services.NewAdminService(users, nil, audit, &fakeMailer{})
		row, apiErr := svc.UpdateUserStatus(context.Background(), "a1", "u1", "Active", "")
		require.Nil(t, apiErr)
		assert.Equal(t, "Active", row.Status)
		assert.Equal(t, []string{"u1:Active"}, users.updateCalls)
		require.Len(t, audit.entries, 1)
		e := audit.entries[0]
		assert.Equal(t, "user.status_changed", e.Action)
		assert.Equal(t, "user", e.TargetType)
		assert.Equal(t, "u1", e.TargetID)
		assert.Equal(t, "a1", e.ActorID)
		assert.Equal(t, map[string]interface{}{"status": "Blocked"}, e.Before)
		assert.Equal(t, map[string]interface{}{"status": "Active"}, e.After)
	})

	t.Run("audit entry carries reason", func(t *testing.T) {
		users, audit := &fakeUsers{detail: userDetail("client", "Active")}, &fakeAudit{}
		svc := services.NewAdminService(users, nil, audit, &fakeMailer{})
		_, apiErr := svc.UpdateUserStatus(context.Background(), "a1", "u1", "Blocked", " abuse ")
		require.Nil(t, apiErr)
		assert.Equal(t, "abuse", audit.entries[0].Reason)
	})

	t.Run("audit failure does not fail the request", func(t *testing.T) {
		users, audit := &fakeUsers{detail: userDetail("client", "Active")}, &fakeAudit{insertErr: errors.New("mongo down")}
		svc := services.NewAdminService(users, nil, audit, &fakeMailer{})
		row, apiErr := svc.UpdateUserStatus(context.Background(), "a1", "u1", "Suspended", "r")
		require.Nil(t, apiErr)
		assert.Equal(t, "Suspended", row.Status)
		assert.Len(t, audit.entries, 1)
	})
}

func TestAdminResendConfirmation(t *testing.T) {
	t.Run("409 unless To Confirm", func(t *testing.T) {
		mailer, audit := &fakeMailer{}, &fakeAudit{}
		svc := services.NewAdminService(&fakeUsers{detail: userDetail("client", "Active")}, nil, audit, mailer)
		apiErr := svc.ResendConfirmation(context.Background(), "a1", "u1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "user already confirmed")
		assert.Empty(t, mailer.confirmEmails)
		assert.Empty(t, audit.entries)
	})

	t.Run("404", func(t *testing.T) {
		svc := services.NewAdminService(&fakeUsers{}, nil, &fakeAudit{}, &fakeMailer{})
		apiErr := svc.ResendConfirmation(context.Background(), "a1", "u1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("sends and audits", func(t *testing.T) {
		mailer, audit := &fakeMailer{}, &fakeAudit{}
		svc := services.NewAdminService(&fakeUsers{detail: userDetail("client", "To Confirm")}, nil, audit, mailer)
		require.Nil(t, svc.ResendConfirmation(context.Background(), "a1", "u1"))
		assert.Equal(t, []string{"u1@x.io"}, mailer.confirmEmails)
		require.Len(t, audit.entries, 1)
		assert.Equal(t, "user.confirmation_resent", audit.entries[0].Action)
	})

	t.Run("mailer 500 is hidden", func(t *testing.T) {
		mailer := &fakeMailer{err: models.NewApiError(http.StatusInternalServerError, errors.New("smtp secret"))}
		svc := services.NewAdminService(&fakeUsers{detail: userDetail("client", "To Confirm")}, nil, &fakeAudit{}, mailer)
		apiErr := svc.ResendConfirmation(context.Background(), "a1", "u1")
		require.NotNil(t, apiErr)
		assert.EqualError(t, apiErr.Error, "internal error")
	})
}

func TestAdminSendPasswordReset(t *testing.T) {
	mailer, audit := &fakeMailer{}, &fakeAudit{}
	svc := services.NewAdminService(&fakeUsers{detail: userDetail("coach", "Active")}, nil, audit, mailer)
	require.Nil(t, svc.SendPasswordReset(context.Background(), "a1", "u1"))
	assert.Equal(t, []string{"u1@x.io"}, mailer.resetEmails)
	require.Len(t, audit.entries, 1)
	e := audit.entries[0]
	assert.Equal(t, "user.password_reset_sent", e.Action)
	assert.Nil(t, e.Before)
	assert.Nil(t, e.After)
	assert.Empty(t, e.Reason)
}

func (f *fakeUsers) ListAdminUsers(_ context.Context, q db.AdminUserQuery) ([]db.AdminUserRow, int, error) {
	f.listedRole = q.Role
	return nil, 0, nil
}

func TestAdminListUsersAcceptsGymOwnerRole(t *testing.T) {
	users := &fakeUsers{}
	svc := services.NewAdminService(users, nil, &fakeAudit{}, &fakeMailer{})
	_, apiErr := svc.ListUsers(context.Background(), db.AdminUserQuery{Role: "gym_owner"})
	require.Nil(t, apiErr)
	assert.Equal(t, "gym_owner", users.listedRole)
}

func TestAdminCreateGymOwner(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name               string
		first, last, email string
		wantCode           int
		wantMsg            string
	}{
		{"missing firstname", " ", "B", "a@b.io", 400, "firstname and lastname are required"},
		{"missing lastname", "A", "", "a@b.io", 400, "firstname and lastname are required"},
		{"bad email", "A", "B", "not-an-email", 400, "invalid email"},
		{"display name email", "A", "B", "Evil <a@b.io>", 400, "invalid email"},
		{"empty email", "A", "B", "", 400, "invalid email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mailer, audit := &fakeMailer{}, &fakeAudit{}
			svc := services.NewAdminService(&fakeUsers{}, nil, audit, mailer)
			row, apiErr := svc.CreateGymOwner(ctx, "a1", tc.first, tc.last, tc.email)
			assert.Nil(t, row)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.wantCode, apiErr.Code)
			assert.EqualError(t, apiErr.Error, tc.wantMsg)
			assert.Empty(t, mailer.createdOwners)
			assert.Empty(t, mailer.resetEmails)
			assert.Empty(t, audit.entries)
		})
	}

	t.Run("email exists is 409 and nothing is sent or audited", func(t *testing.T) {
		mailer, audit := &fakeMailer{createErr: models.NewApiError(http.StatusConflict, models.ErrEmailAlreadyExists)}, &fakeAudit{}
		svc := services.NewAdminService(&fakeUsers{}, nil, audit, mailer)
		row, apiErr := svc.CreateGymOwner(ctx, "a1", "A", "B", "a@b.io")
		assert.Nil(t, row)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Empty(t, mailer.resetEmails)
		assert.Empty(t, audit.entries)
	})

	t.Run("create failure 500 is hidden", func(t *testing.T) {
		mailer := &fakeMailer{createErr: models.NewApiError(http.StatusInternalServerError, errors.New("mongo secret"))}
		svc := services.NewAdminService(&fakeUsers{}, nil, &fakeAudit{}, mailer)
		_, apiErr := svc.CreateGymOwner(ctx, "a1", "A", "B", "a@b.io")
		require.NotNil(t, apiErr)
		assert.EqualError(t, apiErr.Error, "internal error")
	})

	t.Run("success: active verified gym_owner, reset email sent, audited without secrets", func(t *testing.T) {
		mailer, audit := &fakeMailer{}, &fakeAudit{}
		svc := services.NewAdminService(&fakeUsers{}, nil, audit, mailer)
		row, apiErr := svc.CreateGymOwner(ctx, "a1", " Ada ", "Lovelace", " ada@gym.io ")
		require.Nil(t, apiErr)
		assert.Equal(t, "g1", row.ExternalID)
		assert.Equal(t, "gym_owner", row.Role)
		assert.Equal(t, "Active", row.Status)
		assert.True(t, row.VerificationStatus)
		assert.Equal(t, "ada@gym.io", row.Email)
		assert.Equal(t, "Ada", row.Firstname)
		assert.Equal(t, []string{"ada@gym.io"}, mailer.createdOwners)
		assert.Equal(t, []string{"ada@gym.io"}, mailer.resetEmails)
		require.Len(t, audit.entries, 1)
		e := audit.entries[0]
		assert.Equal(t, "gym_owner.created", e.Action)
		assert.Equal(t, "user", e.TargetType)
		assert.Equal(t, "a1", e.ActorID)
		assert.Equal(t, "g1", e.TargetID)
		assert.NotContains(t, e.After, "email")
	})

	t.Run("reset email failure still audits the created account and returns the error", func(t *testing.T) {
		mailer, audit := &fakeMailer{err: models.NewApiError(http.StatusInternalServerError, errors.New("smtp secret"))}, &fakeAudit{}
		svc := services.NewAdminService(&fakeUsers{}, nil, audit, mailer)
		row, apiErr := svc.CreateGymOwner(ctx, "a1", "A", "B", "a@b.io")
		assert.Nil(t, row)
		require.NotNil(t, apiErr)
		assert.EqualError(t, apiErr.Error, "internal error")
		require.Len(t, audit.entries, 1)
		assert.Equal(t, "gym_owner.created", audit.entries[0].Action)
	})
}
