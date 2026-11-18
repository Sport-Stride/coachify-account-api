package repositories

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGymPolicyIndexes(t *testing.T) {
	pol := gymPolicyIndexes()
	require.Len(t, pol, 1)
	assert.Equal(t, "idx_gym_role_policies_gym_role_unique", *pol[0].Options.Name)
	assert.True(t, *pol[0].Options.Unique)

	gr := permissionGrantIndexes()
	require.Len(t, gr, 2)
	assert.Equal(t, "idx_permission_grants_gym_user_cap_unique", *gr[0].Options.Name)
	assert.True(t, *gr[0].Options.Unique)
	assert.Equal(t, "idx_permission_grants_gym_user", *gr[1].Options.Name)
}
