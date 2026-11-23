package db

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Onboarding answers are stored as stable keys, never as display text or list indexes:
// the apps translate them, so renaming a label never touches stored data.
// Only add keys; never rename or remove one (existing users hold them).

// Specialties a coach or nutritionist offers (metadata.specialties).
var Specialties = map[string]bool{
	"strength":        true,
	"weight_loss":     true,
	"fitness":         true,
	"nutrition":       true,
	"athletic_prep":   true,
	"running":         true,
	"online_coaching": true,
	"yoga":            true,
	"injury_recovery": true,
	"seniors":         true,
}

// Goals a client can pick (metadata.goal).
var Goals = map[string]bool{
	"lose_weight":      true,
	"build_muscle":     true,
	"get_fit":          true,
	"competition_prep": true,
}

// Levels a client can pick (metadata.level).
var Levels = map[string]bool{
	"beginner":     true,
	"intermediate": true,
	"advanced":     true,
}

// NormalizeSpecialties returns the specialties without duplicates, in request order,
// and false when one of them is not a known key. An empty list clears the field.
func NormalizeSpecialties(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		if !Specialties[s] {
			return nil, false
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, true
}

// ValidGoal accepts a known goal, or "" to clear it.
func ValidGoal(g string) bool { return g == "" || Goals[g] }

// ValidLevel accepts a known level, or "" to clear it.
func ValidLevel(l string) bool { return l == "" || Levels[l] }

// BirthDateLayout is the stored format of personal.birth_date.
const BirthDateLayout = "2006-01-02"

// ValidBirthDate accepts "" (clear) or a real YYYY-MM-DD date between 1900-01-01 and today.
func ValidBirthDate(s string, now time.Time) bool {
	if s == "" {
		return true
	}
	d, err := time.Parse(BirthDateLayout, s)
	if err != nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return !d.Before(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) && !d.After(today)
}

// Height bounds in centimetres.
const (
	MinHeightCm = 50
	MaxHeightCm = 272
)

// ValidHeightCm accepts 0 (clear) or a height between MinHeightCm and MaxHeightCm.
func ValidHeightCm(h int) bool { return h == 0 || (h >= MinHeightCm && h <= MaxHeightCm) }

// MaxExperienceYears bounds metadata.experience_years.
const MaxExperienceYears = 60

// ValidExperienceYears accepts 0 (clear) up to MaxExperienceYears.
func ValidExperienceYears(y int) bool { return y >= 0 && y <= MaxExperienceYears }

// MaxCertificationLen bounds metadata.certification, in characters.
const MaxCertificationLen = 100

// NormalizeCertification trims the certification and reports false when it is too long
// or holds control characters. "" clears it.
func NormalizeCertification(c string) (string, bool) {
	c = strings.TrimSpace(c)
	if utf8.RuneCountInString(c) > MaxCertificationLen || !utf8.ValidString(c) {
		return "", false
	}
	for _, r := range c {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return c, true
}
