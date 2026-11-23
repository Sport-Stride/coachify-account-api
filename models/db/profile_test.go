package db

import (
	"strings"
	"testing"
	"time"

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

func TestValidBirthDate(t *testing.T) {
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	for _, ok := range []string{"", "1995-04-12", "1900-01-01", "2026-11-23"} {
		assert.True(t, ValidBirthDate(ok, now), ok)
	}
	for _, bad := range []string{"12/04/1995", "1995-02-30", "2026-11-24", "1899-12-31", "1995-4-12"} {
		assert.False(t, ValidBirthDate(bad, now), bad)
	}
}

func TestValidHeightCm(t *testing.T) {
	for _, ok := range []int{0, MinHeightCm, 172, MaxHeightCm} {
		assert.True(t, ValidHeightCm(ok), ok)
	}
	for _, bad := range []int{-1, 1, MinHeightCm - 1, MaxHeightCm + 1} {
		assert.False(t, ValidHeightCm(bad), bad)
	}
}

func TestValidExperienceYears(t *testing.T) {
	for _, ok := range []int{0, 1, 8, MaxExperienceYears} {
		assert.True(t, ValidExperienceYears(ok), ok)
	}
	for _, bad := range []int{-1, MaxExperienceYears + 1} {
		assert.False(t, ValidExperienceYears(bad), bad)
	}
}

func TestNormalizeCertification(t *testing.T) {
	got, ok := NormalizeCertification("  Diplôme d'État  ")
	assert.True(t, ok)
	assert.Equal(t, "Diplôme d'État", got)

	got, ok = NormalizeCertification("")
	assert.True(t, ok)
	assert.Empty(t, got)

	_, ok = NormalizeCertification(strings.Repeat("é", MaxCertificationLen+1))
	assert.False(t, ok, "counted in characters, too long")
	_, ok = NormalizeCertification(strings.Repeat("é", MaxCertificationLen))
	assert.True(t, ok)
	_, ok = NormalizeCertification("line\nbreak")
	assert.False(t, ok)
}
