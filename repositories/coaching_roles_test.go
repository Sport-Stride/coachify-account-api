package repositories

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"coachify-account-api/models/db"
)

func TestCoachingRolesFilterShape(t *testing.T) {
	t.Cleanup(func() { viper.Set(db.GymOwnerAsCoachFlag, nil) })

	viper.Set(db.GymOwnerAsCoachFlag, false)
	assert.Equal(t, []string{"coach", "nutritionist"}, coachingRoles())

	viper.Set(db.GymOwnerAsCoachFlag, true)
	assert.Equal(t, []string{"coach", "nutritionist", "gym_owner"}, coachingRoles())
}
