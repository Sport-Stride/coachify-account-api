package db

import "time"

// Default notification preferences, served while a user has never saved theirs.
const (
	DefaultReminderTime = "20:00"
	DefaultTimezone     = "Africa/Tunis"
)

// NotificationPrefs are a user's push preferences (collection notification_prefs, _id = user external ID).
// Messages covers new chat messages; Reminders covers session and meeting reminders and the coach's morning
// summary, sent at ReminderTime ("HH:MM") in Timezone (IANA).
type NotificationPrefs struct {
	UserID       string     `bson:"_id" json:"-"`
	Messages     bool       `bson:"messages" json:"messages"`
	Reminders    bool       `bson:"reminders" json:"reminders"`
	ReminderTime string     `bson:"reminder_time" json:"reminder_time"`
	Timezone     string     `bson:"timezone" json:"timezone"`
	UpdatedAt    *time.Time `bson:"updated_at,omitempty" json:"updated_at,omitempty"`
}

// DefaultNotificationPrefs returns the preferences of a user who never saved any.
func DefaultNotificationPrefs(userID string) NotificationPrefs {
	return NotificationPrefs{UserID: userID, Messages: true, Reminders: true, ReminderTime: DefaultReminderTime, Timezone: DefaultTimezone}
}

// NotificationPrefsPatch holds the fields a PUT changes; nil fields keep their stored (or default) value.
type NotificationPrefsPatch struct {
	Messages     *bool
	Reminders    *bool
	ReminderTime *string
	Timezone     *string
}
