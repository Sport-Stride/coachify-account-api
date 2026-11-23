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

type fakePushStore struct {
	upserts []db.PushDevice
	deletes [][2]string
	trimmed []int64
	list    []db.PushDevice
	err     error
	trimErr error
	removed []string
}

func (f *fakePushStore) DeleteTokens(_ context.Context, tokens []string) (int64, error) {
	f.removed = append(f.removed, tokens...)
	return int64(len(tokens)), f.err
}

func (f *fakePushStore) Upsert(_ context.Context, d db.PushDevice) error {
	f.upserts = append(f.upserts, d)
	return f.err
}
func (f *fakePushStore) ListByUser(_ context.Context, _ string, _ int64) ([]db.PushDevice, error) {
	return f.list, f.err
}
func (f *fakePushStore) DeleteForUser(_ context.Context, userID, token string) error {
	f.deletes = append(f.deletes, [2]string{userID, token})
	return f.err
}
func (f *fakePushStore) TrimUser(_ context.Context, _ string, keep int64) error {
	f.trimmed = append(f.trimmed, keep)
	return f.trimErr
}

const goodToken = "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"

func TestPushDevices_Register(t *testing.T) {
	st := &fakePushStore{}
	apiErr := services.NewPushDeviceService(st).Register(context.Background(), "u1", api.RegisterDeviceRequest{Token: goodToken, Platform: "ios", Locale: "fr-TN"})
	require.Nil(t, apiErr)
	require.Len(t, st.upserts, 1)
	assert.Equal(t, db.PushDevice{Token: goodToken, UserID: "u1", Platform: "ios", Locale: "fr-TN"}, st.upserts[0])
	assert.Equal(t, []int64{services.MaxDevicesPerUser}, st.trimmed)

	// New-style Expo token, no locale.
	st = &fakePushStore{}
	require.Nil(t, services.NewPushDeviceService(st).Register(context.Background(), "u1", api.RegisterDeviceRequest{Token: "ExpoPushToken[abcdefghij_KLM-123]", Platform: "android"}))
	assert.Len(t, st.upserts, 1)
}

func TestPushDevices_RegisterRejects(t *testing.T) {
	cases := map[string]api.RegisterDeviceRequest{
		"empty token":     {Platform: "ios"},
		"raw fcm token":   {Token: "fcm:APA91bHPRgkF3JUikC4ENAHEeMrd41Zxv3hVZjC9KtT8OvPVGJ", Platform: "android"},
		"injected token":  {Token: "ExponentPushToken[abc]\"},{\"$ne\":1", Platform: "ios"},
		"short token":     {Token: "ExponentPushToken[abc]", Platform: "ios"},
		"bad platform":    {Token: goodToken, Platform: "web"},
		"bad locale":      {Token: goodToken, Platform: "ios", Locale: "fr; DROP"},
		"too long locale": {Token: goodToken, Platform: "ios", Locale: "fr-aaaaaaaaa-bb"},
	}
	for name, req := range cases {
		st := &fakePushStore{}
		apiErr := services.NewPushDeviceService(st).Register(context.Background(), "u1", req)
		require.NotNil(t, apiErr, name)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code, name)
		assert.Empty(t, st.upserts, name)
	}
}

func TestPushDevices_StoreErrors(t *testing.T) {
	st := &fakePushStore{err: errors.New("mongo down")}
	apiErr := services.NewPushDeviceService(st).Register(context.Background(), "u1", api.RegisterDeviceRequest{Token: goodToken, Platform: "ios"})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.Empty(t, st.trimmed)

	trimFails := &fakePushStore{trimErr: errors.New("mongo slow")}
	assert.Nil(t, services.NewPushDeviceService(trimFails).Register(context.Background(), "u1", api.RegisterDeviceRequest{Token: goodToken, Platform: "ios"}), "a failed trim does not fail the registration")
}

func TestPushDevices_Unregister(t *testing.T) {
	st := &fakePushStore{}
	svc := services.NewPushDeviceService(st)
	require.Nil(t, svc.Unregister(context.Background(), "u1", goodToken))
	assert.Equal(t, [][2]string{{"u1", goodToken}}, st.deletes, "deletes only the caller's token")

	apiErr := svc.Unregister(context.Background(), "u1", "nope")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
}

func TestPushDevices_List(t *testing.T) {
	st := &fakePushStore{list: []db.PushDevice{{Token: goodToken, UserID: "u1", Platform: "ios"}}}
	devices, apiErr := services.NewPushDeviceService(st).List(context.Background(), "u1")
	require.Nil(t, apiErr)
	assert.Len(t, devices, 1)

	_, apiErr = services.NewPushDeviceService(st).List(context.Background(), "")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
}

func TestPushDevices_RemoveInvalid(t *testing.T) {
	st := &fakePushStore{}
	n, apiErr := services.NewPushDeviceService(st).RemoveInvalid(context.Background(), []string{goodToken})
	require.Nil(t, apiErr)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, []string{goodToken}, st.removed)

	big := make([]string, services.MaxInvalidTokens+1)
	for i := range big {
		big[i] = goodToken
	}
	for name, tokens := range map[string][]string{"none": nil, "too many": big, "not expo": {goodToken, "abc"}} {
		st := &fakePushStore{}
		_, apiErr := services.NewPushDeviceService(st).RemoveInvalid(context.Background(), tokens)
		require.NotNil(t, apiErr, name)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code, name)
		assert.Empty(t, st.removed, name)
	}

	_, apiErr = services.NewPushDeviceService(&fakePushStore{err: errors.New("down")}).RemoveInvalid(context.Background(), []string{goodToken})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}
