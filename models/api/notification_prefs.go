package api

import "coachify-account-api/models/db"

// UpdateNotificationPrefsRequest is the PUT /user/notification-prefs body; omitted fields are unchanged.
type UpdateNotificationPrefsRequest struct {
	Messages     *bool   `json:"messages"`
	Reminders    *bool   `json:"reminders"`
	ReminderTime *string `json:"reminder_time"`
	Timezone     *string `json:"timezone"`
}

// NotificationPrefsLookupRequest is the POST /internal/notification-prefs body.
type NotificationPrefsLookupRequest struct {
	UserIDs []string `json:"user_ids"`
}

// NotificationPrefsLookupResponse maps each requested user to their preferences (defaults when never saved).
type NotificationPrefsLookupResponse struct {
	Prefs map[string]db.NotificationPrefs `json:"prefs"`
}
