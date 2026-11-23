package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeSpecialties(t *testing.T) {
	got, ok := NormalizeSpecialties([]string{"yoga", "strength", "yoga"})
	assert.True(t, ok)
	assert.Equal(t, []string{"yoga", "strength"}, got, "duplicates dropped, order kept")

	got, ok = NormalizeSpecialties(nil)
	assert.True(t, ok)
	assert.Empty(t, got, "an empty list clears the field")

	_, ok = NormalizeSpecialties([]string{"strength", "Musculation"})
	assert.False(t, ok, "display text is not a key")

	_, ok = NormalizeSpecialties([]string{"0"})
	assert.False(t, ok, "list indexes are not keys")
}

func TestValidGoalAndLevel(t *testing.T) {
	for g := range Goals {
		assert.True(t, ValidGoal(g), g)
	}
	for l := range Levels {
		assert.True(t, ValidLevel(l), l)
	}
	assert.True(t, ValidGoal(""), "empty clears")
	assert.True(t, ValidLevel(""), "empty clears")
	assert.False(t, ValidGoal("Perdre du poids"))
	assert.False(t, ValidLevel("expert"))
}

// The apps map their option lists onto these keys: changing the counts breaks them.
func TestOnboardingKeyCounts(t *testing.T) {
	assert.Len(t, Specialties, 10)
	assert.Len(t, Goals, 4)
	assert.Len(t, Levels, 3)
}
