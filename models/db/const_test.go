package db

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func setOwnerAsCoach(t *testing.T, on bool) {
	t.Helper()
	viper.Set(GymOwnerAsCoachFlag, on)
	t.Cleanup(func() { viper.Set(GymOwnerAsCoachFlag, nil) })
}

func TestIsCoachingRole(t *testing.T) {
	cases := []struct {
		role    string
		off, on bool
	}{
		{"coach", true, true},
		{RoleGymOwner, false, true},
		{"nutritionist", false, false},
		{"client", false, false},
		{"admin", false, false},
		{"head_coach", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		setOwnerAsCoach(t, false)
		assert.Equal(t, tc.off, IsCoachingRole(tc.role), "flag off, role %q", tc.role)
		setOwnerAsCoach(t, true)
		assert.Equal(t, tc.on, IsCoachingRole(tc.role), "flag on, role %q", tc.role)
	}
}

func TestIsCoachingRoleDefaultsOff(t *testing.T) {
	viper.Set(GymOwnerAsCoachFlag, nil)
	assert.False(t, IsCoachingRole(RoleGymOwner))
}

func TestIsPaymentUnlockedGymOwner(t *testing.T) {
	approved := func(role string) *User {
		return &User{UserRole: role, MatriculeFiscaleStatus: MatriculeFiscaleApproved}
	}
	setOwnerAsCoach(t, false)
	assert.False(t, approved(RoleGymOwner).IsPaymentUnlocked())
	assert.True(t, approved("coach").IsPaymentUnlocked())
	assert.True(t, approved("nutritionist").IsPaymentUnlocked())
	setOwnerAsCoach(t, true)
	assert.True(t, approved(RoleGymOwner).IsPaymentUnlocked())
	assert.False(t, (&User{UserRole: RoleGymOwner, MatriculeFiscaleStatus: MatriculeFiscalePending}).IsPaymentUnlocked())
	assert.False(t, approved("client").IsPaymentUnlocked())
	var nilUser *User
	assert.False(t, nilUser.IsPaymentUnlocked())
}

func TestIsMatriculeRole(t *testing.T) {
	for _, on := range []bool{false, true} {
		setOwnerAsCoach(t, on)
		assert.True(t, IsMatriculeRole("coach"))
		assert.True(t, IsMatriculeRole("nutritionist"))
		assert.Equal(t, on, IsMatriculeRole(RoleGymOwner), "gym_owner target, flag on=%v", on)
		assert.False(t, IsMatriculeRole("client"))
		assert.False(t, IsMatriculeRole("admin"))
	}
}
