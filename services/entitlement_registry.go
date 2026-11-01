package services

import (
	"sort"
	"time"
)

// Entitlement sources reported per feature.
const (
	SourceDefault   = "default"
	SourcePlan      = "plan"
	SourceOverride  = "override"
	SourceCoachCap  = "coach_cap"
	SourceNotApplic = "n/a"
	SourceParent    = "parent"
)

// Feature keys.
const (
	FeatureWorkouts    = "workouts"
	FeatureNutrition   = "nutrition"
	FeatureChat        = "chat"
	FeatureInvitations = "invitations"
	FeatureFileManager = "file_manager"

	// Children of the workouts master switch.
	FeatureExercises        = "exercises"
	FeatureWorkoutTemplates = "workout_templates"
	FeaturePrograms         = "programs"
	FeatureTasks            = "tasks"

	// Children of the nutrition master switch.
	FeatureRecipes      = "recipes"
	FeatureIngredients  = "ingredients"
	FeatureMealPlans    = "meal_plans"
	FeatureRecipeBooks  = "recipe_books"
	FeatureMealPrograms = "meal_programs"
)

// Limit keys.
const (
	LimitMaxClients = "max_clients"
	LimitMaxUploads = "max_uploads"
	LimitMaxBytes   = "max_bytes"

	LimitMaxMessagesMonth = "max_messages_month"
)

// FeatureDef declares a feature: who it applies to, its default and the limits it supports.
// Adding a feature is one entry here plus one RequireFeature call in the consuming service.
type FeatureDef struct {
	Key            string   `json:"key"`
	AppliesTo      []string `json:"applies_to"`
	DefaultEnabled bool     `json:"default_enabled"`
	Limits         []string `json:"limits"`
	// Parent is the master-switch feature: a child is enabled only while its parent is.
	Parent string `json:"parent,omitempty"`
}

var coachAndClient = []string{"coach", "client"}

// featureRegistry is code, not data. Defaults: every feature on, every limit unlimited.
func child(key, parent string) FeatureDef {
	return FeatureDef{Key: key, AppliesTo: coachAndClient, DefaultEnabled: true, Limits: []string{}, Parent: parent}
}

var featureRegistry = map[string]FeatureDef{
	FeatureWorkouts:    {Key: FeatureWorkouts, AppliesTo: coachAndClient, DefaultEnabled: true, Limits: []string{}},
	FeatureNutrition:   {Key: FeatureNutrition, AppliesTo: coachAndClient, DefaultEnabled: true, Limits: []string{}},
	FeatureChat:        {Key: FeatureChat, AppliesTo: coachAndClient, DefaultEnabled: true, Limits: []string{LimitMaxMessagesMonth}},
	FeatureInvitations: {Key: FeatureInvitations, AppliesTo: []string{"coach"}, DefaultEnabled: true, Limits: []string{LimitMaxClients}},
	FeatureFileManager: {Key: FeatureFileManager, AppliesTo: coachAndClient, DefaultEnabled: true, Limits: []string{LimitMaxUploads, LimitMaxBytes}},

	FeatureExercises:        child(FeatureExercises, FeatureWorkouts),
	FeatureWorkoutTemplates: child(FeatureWorkoutTemplates, FeatureWorkouts),
	FeaturePrograms:         child(FeaturePrograms, FeatureWorkouts),
	FeatureTasks:            child(FeatureTasks, FeatureWorkouts),

	FeatureRecipes:      child(FeatureRecipes, FeatureNutrition),
	FeatureIngredients:  child(FeatureIngredients, FeatureNutrition),
	FeatureMealPlans:    child(FeatureMealPlans, FeatureNutrition),
	FeatureRecipeBooks:  child(FeatureRecipeBooks, FeatureNutrition),
	FeatureMealPrograms: child(FeatureMealPrograms, FeatureNutrition),
}

// limitCounters maps a limit key to the usage counter it bounds. Limits without a counter
// (max_clients) are checked against live data instead.
var limitCounters = map[string]string{
	LimitMaxUploads: "uploads",
	LimitMaxBytes:   "bytes",

	LimitMaxMessagesMonth: "messages",
}

// counterLimits is the inverse of limitCounters.
var counterLimits = map[string]string{
	"uploads": LimitMaxUploads,
	"bytes":   LimitMaxBytes,

	"messages": LimitMaxMessagesMonth,
}

// monthlyCounters are counters stored per UTC month (key "<counter>_<YYYYMM>") so they reset by key.
var monthlyCounters = map[string]bool{"messages": true}

// CounterKey returns the storage key of a usage counter at time t.
func CounterKey(counter string, t time.Time) string {
	if monthlyCounters[counter] {
		return counter + "_" + t.UTC().Format("200601")
	}
	return counter
}

// Features returns the registry parents-first: top-level features sorted by key, each followed by its children sorted by key.
func Features() []FeatureDef {
	var tops []FeatureDef
	children := map[string][]FeatureDef{}
	for _, d := range featureRegistry {
		if d.Parent == "" {
			tops = append(tops, d)
		} else {
			children[d.Parent] = append(children[d.Parent], d)
		}
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].Key < tops[j].Key })
	out := make([]FeatureDef, 0, len(featureRegistry))
	for _, d := range tops {
		out = append(out, d)
		kids := children[d.Key]
		sort.Slice(kids, func(i, j int) bool { return kids[i].Key < kids[j].Key })
		out = append(out, kids...)
	}
	return out
}

func (d FeatureDef) appliesTo(role string) bool {
	for _, r := range d.AppliesTo {
		if r == role {
			return true
		}
	}
	return false
}

func (d FeatureDef) hasLimit(limit string) bool {
	for _, l := range d.Limits {
		if l == limit {
			return true
		}
	}
	return false
}

// entitlementRole maps an account role to its entitlement role: coach (coach, nutritionist),
// client, bypass (admin, gym_owner) or "" (not applicable).
func entitlementRole(role string) string {
	switch role {
	case "coach", "nutritionist":
		return "coach"
	case "client":
		return "client"
	case "admin", "gym_owner":
		return "bypass"
	}
	return ""
}
