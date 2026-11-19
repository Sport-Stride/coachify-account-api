package handlers_test

import (
	"net/http"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

func ownerAsCoach(t *testing.T, on bool) {
	t.Helper()
	viper.Set(db.GymOwnerAsCoachFlag, on)
	t.Cleanup(func() { viper.Set(db.GymOwnerAsCoachFlag, nil) })
}

func TestGymOwnerAsCoachMatriculeGates(t *testing.T) {
	const body = `{"matricule_fiscale":"123"}`
	for _, on := range []bool{false, true} {
		ownerAsCoach(t, on)
		want := http.StatusForbidden
		if on {
			want = http.StatusOK
		}

		s := newSvcs()
		s.auth.On("SubmitMatriculeFiscale", mock.Anything, "o1", "123").Return(nil)
		assert.Equal(t, want, do(newAPIRouter(s, "o1", "gym_owner"), "PATCH", "/user/matricule-fiscale", body).Code, "submit on=%v", on)

		s = newSvcs()
		s.auth.On("WithdrawTaxRegistration", mock.Anything, "o1").Return(nil)
		assert.Equal(t, want, do(newAPIRouter(s, "o1", "gym_owner"), "DELETE", "/user/tax-registration", "").Code, "withdraw on=%v", on)

		// other roles are never widened
		assert.Equal(t, http.StatusForbidden, do(newAPIRouter(newSvcs(), "u1", "client"), "PATCH", "/user/matricule-fiscale", body).Code)
	}
}

func TestGymOwnerAsCoachCheckClientLinks(t *testing.T) {
	for _, on := range []bool{false, true} {
		ownerAsCoach(t, on)
		s := newSvcs()
		s.coach.On("CheckClientLinks", mock.Anything, "o1", []string{"c1"}).Return(map[string]bool{"c1": true}, nil)
		w := do(newAPIRouter(s, "o1", "gym_owner"), http.MethodPost, "/coach/clients/check", `{"client_ids":["c1"]}`)
		if on {
			assert.Equal(t, http.StatusOK, w.Code)
		} else {
			assert.Equal(t, http.StatusForbidden, w.Code)
			s.coach.AssertNotCalled(t, "CheckClientLinks", mock.Anything, mock.Anything, mock.Anything)
		}
	}
}

func TestGymOwnerAsCoachClientAccess(t *testing.T) {
	for _, on := range []bool{false, true} {
		ownerAsCoach(t, on)
		s := newSvcs()
		s.ent.On("ListClientAccess", mock.Anything, "o1", mock.Anything, mock.Anything, mock.Anything).
			Return(&services.ClientAccessList{}, nil)
		w := get(newAPIRouter(s, "o1", "gym_owner"), "/coach/clients/access")
		if on {
			assert.Equal(t, http.StatusOK, w.Code)
		} else {
			assert.Equal(t, http.StatusForbidden, w.Code)
		}
	}
}

// Gym membership and head-coach routes never accept a gym_owner, flag on or off.
func TestGymOwnerAsCoachDoesNotWidenMembership(t *testing.T) {
	ownerAsCoach(t, true)
	for _, rt := range []struct{ method, path string }{
		{"GET", "/coach/gym-invitations"},
		{"POST", "/coach/gym-invitations/i1/accept"},
		{"GET", "/head-coach/gym"},
	} {
		w := do(newGymRouter(&fakeGym{}, "o1", "gym_owner"), rt.method, rt.path, "")
		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s", rt.method, rt.path)
	}
}

func TestGymOwnerRegistrationLink(t *testing.T) {
	for _, on := range []bool{false, true} {
		ownerAsCoach(t, on)
		s := newSvcs()
		s.reg.On("GetOrCreateLink", mock.Anything, "o1").Return(&db.RegistrationLink{Token: "tok", CoachID: "o1"}, nil)
		w := do(newAPIRouter(s, "o1", "gym_owner"), "POST", "/coach/registration-link", "")
		if on {
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "o1", decode(t, w)["coach_id"])
			s.reg.AssertExpectations(t)
		} else {
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Equal(t, "Coach access required", decode(t, w)["error"])
			s.reg.AssertNotCalled(t, "GetOrCreateLink", mock.Anything, mock.Anything)
		}
		// a coach is unaffected by the flag
		s = newSvcs()
		s.reg.On("GetOrCreateLink", mock.Anything, "c1").Return(&db.RegistrationLink{Token: "t", CoachID: "c1"}, nil)
		assert.Equal(t, http.StatusOK, do(newAPIRouter(s, "c1", "coach"), "POST", "/coach/registration-link", "").Code)
	}
}
