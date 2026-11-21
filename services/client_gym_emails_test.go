package services_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

type inviteMail struct{ email, gym, inviter, link string }

type fakeInviteMailer struct {
	mu   sync.Mutex
	sent []inviteMail
	err  error
}

func (m *fakeInviteMailer) SendGymClientInviteEmail(_ context.Context, email, gym, inviter, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, inviteMail{email, gym, inviter, link})
	return m.err
}

func TestInviteCreateEmailsClientGymInvite(t *testing.T) {
	ctx := context.Background()
	owner := services.Actor{ID: "owner", Role: db.RoleGymOwner}

	t.Run("email-bound client_gym link is mailed once with the link", func(t *testing.T) {
		f := newInviteFixture(t, true)
		res, apiErr := f.svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA, Email: "n1@x.tn"})
		require.Nil(t, apiErr)
		require.Len(t, f.mailer.sent, 1)
		m := f.mailer.sent[0]
		assert.Equal(t, "n1@x.tn", m.email)
		assert.Equal(t, "Gym A", m.gym)
		assert.Equal(t, "Coach owner", m.inviter)
		assert.Equal(t, res.Link, m.link)
	})
	t.Run("open link and other kinds are not mailed", func(t *testing.T) {
		f := newInviteFixture(t, true)
		_, apiErr := f.svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA})
		require.Nil(t, apiErr)
		_, apiErr = f.svc.Create(ctx, services.Actor{ID: "c1", Role: "coach"}, services.CreateInvite{Kind: db.InviteKindClientPersonal, Email: "n2@x.tn"})
		require.Nil(t, apiErr)
		assert.Empty(t, f.mailer.sent)
	})
	t.Run("mail failure does not fail Create and logs no address or link", func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		restore := zap.ReplaceGlobals(zap.New(core))
		defer restore()
		f := newInviteFixture(t, true)
		f.mailer.err = errors.New("boom")
		res, apiErr := f.svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA, Email: "n1@x.tn"})
		require.Nil(t, apiErr)
		require.NotNil(t, res)
		require.Len(t, f.mailer.sent, 1)
		for _, e := range logs.All() {
			assert.NotContains(t, e.Message, "n1@x.tn")
			for _, fld := range e.Context {
				assert.NotContains(t, fld.String, "n1@x.tn")
				assert.NotContains(t, fld.String, res.Link)
			}
		}
	})
}

type welcomeMail struct{ email, subject, message string }

type fakeWelcomeMailer struct {
	sent []welcomeMail
	err  error
}

func (m *fakeWelcomeMailer) SendGymAssignmentEmail(_ context.Context, email, subject, message string) error {
	m.sent = append(m.sent, welcomeMail{email, subject, message})
	return m.err
}

type fakeWelcomeUsers map[string]*db.User

func (f fakeWelcomeUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrUserNotFound}
}

type namedGymLookup struct{}

func (namedGymLookup) GetGym(_ context.Context, _ string) (*db.Gym, error) {
	return &db.Gym{Name: "Gym A", OwnerID: "owner"}, nil
}

func TestGymClientJoinWelcomeEmail(t *testing.T) {
	ctx := context.Background()
	members := &fakeAuthzMembers{rows: []db.GymMember{member("owner", db.GymMemberRoleOwner), member("c1", db.GymMemberRoleCoach)}}
	quota := &mocks.QuotaGuard{}
	quota.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	users := fakeWelcomeUsers{
		"cl":    {UserEmail: "cl@x.tn", UserFirstname: "Cli"},
		"owner": {UserEmail: "o@x.tn", UserFirstname: "Olga", UserLastname: "Owner"},
		"c1":    {UserEmail: "c1@x.tn", UserFirstname: "Cole", UserLastname: "Coach"},
	}
	build := func(store *fakeGymClientStore, mailer *fakeWelcomeMailer) services.GymClientService {
		return services.NewGymClientService(services.GymClientDeps{
			Members: members, Store: store, Audit: &fakeClientAudit{}, Quota: quota,
			Gyms: namedGymLookup{}, Mailer: mailer, Users: users, Spawn: func(f func()) { f() },
		}, true)
	}
	in := services.JoinGymClientInput{GymID: gymA, ClientID: "cl", CoachID: "c1", Consent: true}

	t.Run("sent once with gym, owner and coach", func(t *testing.T) {
		mailer := &fakeWelcomeMailer{}
		_, apiErr := build(&fakeGymClientStore{}, mailer).Join(ctx, in)
		require.Nil(t, apiErr)
		require.Len(t, mailer.sent, 1)
		m := mailer.sent[0]
		assert.Equal(t, "cl@x.tn", m.email)
		assert.Equal(t, "Welcome to Gym A", m.subject)
		assert.Contains(t, m.message, "Gym A")
		assert.Contains(t, m.message, "Olga Owner")
		assert.Contains(t, m.message, "Cole Coach")
		assert.Contains(t, m.message, "Memberships")
	})
	t.Run("not sent on 409", func(t *testing.T) {
		mailer := &fakeWelcomeMailer{}
		svc := build(&fakeGymClientStore{}, mailer)
		_, apiErr := svc.Join(ctx, in)
		require.Nil(t, apiErr)
		_, apiErr = svc.Join(ctx, in)
		requireCode(t, apiErr, http.StatusConflict)
		assert.Len(t, mailer.sent, 1)
	})
	t.Run("failure does not fail Join and the email is not logged", func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		restore := zap.ReplaceGlobals(zap.New(core))
		defer restore()
		mailer := &fakeWelcomeMailer{err: errors.New("boom")}
		_, apiErr := build(&fakeGymClientStore{}, mailer).Join(ctx, in)
		require.Nil(t, apiErr)
		require.Len(t, mailer.sent, 1)
		require.NotEmpty(t, logs.All())
		for _, e := range logs.All() {
			assert.NotContains(t, e.Message, "cl@x.tn")
			for _, fld := range e.Context {
				assert.NotContains(t, fld.String, "cl@x.tn")
			}
		}
	})
}
