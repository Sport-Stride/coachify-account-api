package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakePrefsStore struct {
	saved   map[string]db.NotificationPrefs
	patches []db.NotificationPrefsPatch
	listed  []string
	err     error
}

func (f *fakePrefsStore) Get(_ context.Context, userID string) (*db.NotificationPrefs, error) {
	if f.err != nil {
		return nil, f.err
	}
	if p, ok := f.saved[userID]; ok {
		return &p, nil
	}
	return nil, nil
}

func (f *fakePrefsStore) Patch(_ context.Context, userID string, p db.NotificationPrefsPatch) (db.NotificationPrefs, error) {
	f.patches = append(f.patches, p)
	out := db.DefaultNotificationPrefs(userID)
	if p.Reminders != nil {
		out.Reminders = *p.Reminders
	}
	return out, f.err
}

func (f *fakePrefsStore) ListByIDs(_ context.Context, ids []string) ([]db.NotificationPrefs, error) {
	f.listed = ids
	out := []db.NotificationPrefs{}
	for _, id := range ids {
		if p, ok := f.saved[id]; ok {
			out = append(out, p)
		}
	}
	return out, f.err
}

func ptrBool(b bool) *bool    { return &b }
func ptrStr(s string) *string { return &s }

func TestNotificationPrefs_GetDefaults(t *testing.T) {
	got, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{}).Get(context.Background(), "u1")
	require.Nil(t, apiErr)
	assert.Equal(t, db.NotificationPrefs{UserID: "u1", Messages: true, Reminders: true, ReminderTime: "20:00", Timezone: "Africa/Tunis"}, got)
}

func TestNotificationPrefs_GetSaved(t *testing.T) {
	saved := db.NotificationPrefs{UserID: "u1", Messages: false, Reminders: true, ReminderTime: "07:30", Timezone: "Europe/Paris"}
	got, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{saved: map[string]db.NotificationPrefs{"u1": saved}}).Get(context.Background(), "u1")
	require.Nil(t, apiErr)
	assert.Equal(t, saved, got)
}

func TestNotificationPrefs_GetStoreError(t *testing.T) {
	_, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{err: errors.New("down")}).Get(context.Background(), "u1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}

func TestNotificationPrefs_UpdatePassesOnlySentFields(t *testing.T) {
	st := &fakePrefsStore{}
	got, apiErr := services.NewNotificationPrefsService(st).Update(context.Background(), "u1", api.UpdateNotificationPrefsRequest{
		Reminders: ptrBool(false), ReminderTime: ptrStr("06:45"), Timezone: ptrStr("Africa/Tunis"),
	})
	require.Nil(t, apiErr)
	assert.False(t, got.Reminders)
	require.Len(t, st.patches, 1)
	p := st.patches[0]
	assert.Nil(t, p.Messages)
	assert.Equal(t, false, *p.Reminders)
	assert.Equal(t, "06:45", *p.ReminderTime)
	assert.Equal(t, "Africa/Tunis", *p.Timezone)
}

func TestNotificationPrefs_UpdateValidation(t *testing.T) {
	cases := map[string]api.UpdateNotificationPrefsRequest{
		"empty":        {},
		"hour 24":      {ReminderTime: ptrStr("24:00")},
		"no colon":     {ReminderTime: ptrStr("2000")},
		"one digit":    {ReminderTime: ptrStr("7:30")},
		"minute 60":    {ReminderTime: ptrStr("07:60")},
		"unknown zone": {Timezone: ptrStr("Mars/Olympus")},
		"local":        {Timezone: ptrStr("Local")},
		"empty zone":   {Timezone: ptrStr("")},
		"path in zone": {Timezone: ptrStr("../../etc/passwd")},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			st := &fakePrefsStore{}
			_, apiErr := services.NewNotificationPrefsService(st).Update(context.Background(), "u1", req)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Empty(t, st.patches)
		})
	}
}

func TestNotificationPrefs_UpdateAcceptsBoundaries(t *testing.T) {
	for _, hm := range []string{"00:00", "23:59", "20:00"} {
		_, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{}).Update(context.Background(), "u1", api.UpdateNotificationPrefsRequest{ReminderTime: ptrStr(hm)})
		assert.Nil(t, apiErr, hm)
	}
	for _, tz := range []string{"UTC", "America/New_York", "Asia/Riyadh"} {
		_, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{}).Update(context.Background(), "u1", api.UpdateNotificationPrefsRequest{Timezone: ptrStr(tz)})
		assert.Nil(t, apiErr, tz)
	}
}

func TestNotificationPrefs_LookupFillsDefaultsAndDedupes(t *testing.T) {
	saved := db.NotificationPrefs{UserID: "u2", Messages: false, Reminders: false, ReminderTime: "08:00", Timezone: "UTC"}
	st := &fakePrefsStore{saved: map[string]db.NotificationPrefs{"u2": saved}}
	got, apiErr := services.NewNotificationPrefsService(st).Lookup(context.Background(), []string{"u1", "u2", "u1"})
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"u1", "u2"}, st.listed)
	assert.Equal(t, db.DefaultNotificationPrefs("u1"), got["u1"])
	assert.Equal(t, saved, got["u2"])
	assert.Len(t, got, 2)
}

func TestNotificationPrefs_LookupValidation(t *testing.T) {
	big := make([]string, services.MaxNotificationPrefsLookup+1)
	for i := range big {
		big[i] = "u"
	}
	for name, ids := range map[string][]string{"none": nil, "too many": big, "blank id": {"u1", ""}, "bad id": {"u1", "a b"}, "operator": {"$ne"}} {
		t.Run(name, func(t *testing.T) {
			st := &fakePrefsStore{}
			_, apiErr := services.NewNotificationPrefsService(st).Lookup(context.Background(), ids)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Nil(t, st.listed)
		})
	}
}

func TestNotificationPrefs_LookupStoreError(t *testing.T) {
	_, apiErr := services.NewNotificationPrefsService(&fakePrefsStore{err: errors.New("down")}).Lookup(context.Background(), []string{"u1"})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}
