package db

import "time"

// PushDevice is an Expo push token registered by a signed-in app (collection push_devices).
// A token belongs to one user at a time: the last one to register it on that device.
type PushDevice struct {
	Token     string    `bson:"token" json:"token"`
	UserID    string    `bson:"user_id" json:"-"`
	Platform  string    `bson:"platform" json:"platform"`
	Locale    string    `bson:"locale,omitempty" json:"locale,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
