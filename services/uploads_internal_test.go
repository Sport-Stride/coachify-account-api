package services

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/utils"
)

type fakeUploadQuota struct {
	got []api.UsageDeltaRequest
	err *models.ApiError
}

func (f *fakeUploadQuota) Consume(_ context.Context, _ string, req api.UsageDeltaRequest) (map[string]int64, *models.ApiError) {
	f.got = append(f.got, req)
	return nil, f.err
}

var uploadCfg = utils.UploadsConfig{CloudName: "demo", APIKey: "123456", APISecret: "s3cret"}

func newTestUploads(cfg utils.UploadsConfig, q UploadQuota) *uploadService {
	s := NewUploadService(cfg, q).(*uploadService)
	s.now = func() time.Time { return time.Unix(1795461000, 0) }
	return s
}

// Example from Cloudinary's "Generating authentication signatures" documentation.
func TestCloudinarySignature_DocumentedVector(t *testing.T) {
	got := cloudinarySignature(map[string]string{
		"timestamp": "1315060510",
		"public_id": "sample_image",
		"eager":     "w_400,h_300,c_pad|w_260,h_200,c_crop",
	}, "abcd")
	assert.Equal(t, "bfd09f95f331f558cbd1320e67aa8d488770583e", got)
}

func TestUploadSign_Success(t *testing.T) {
	q := &fakeUploadQuota{}
	sig, apiErr := newTestUploads(uploadCfg, q).Sign(context.Background(), "u1", api.UploadSignRequest{Purpose: "progress", Size: 1234})
	require.Nil(t, apiErr)
	assert.Equal(t, "https://api.cloudinary.com/v1_1/demo/image/upload", sig.UploadURL)
	assert.Equal(t, int64(1795461000+3600), sig.ExpiresAt)
	assert.Equal(t, "users/u1/progress", sig.Fields["folder"])
	assert.Equal(t, "1795461000", sig.Fields["timestamp"])
	assert.Equal(t, "123456", sig.Fields["api_key"])
	assert.Equal(t, uploadFormats, sig.Fields["allowed_formats"])
	assert.Equal(t, uploadTransformation, sig.Fields["transformation"])
	assert.NotContains(t, sig.Fields, "api_secret")

	signed := map[string]string{}
	for k, v := range sig.Fields {
		if k != "api_key" && k != "signature" {
			signed[k] = v
		}
	}
	assert.Equal(t, cloudinarySignature(signed, "s3cret"), sig.Fields["signature"], "every other field is signed")

	require.Len(t, q.got, 1)
	assert.Equal(t, FeatureFileManager, q.got[0].Feature)
	assert.Equal(t, map[string]int64{"uploads": 1, "bytes": 1234}, q.got[0].Deltas)
}

func TestUploadSign_EachPurposeHasItsFolder(t *testing.T) {
	for purpose, folder := range UploadPurposes {
		sig, apiErr := newTestUploads(uploadCfg, nil).Sign(context.Background(), "u9", api.UploadSignRequest{Purpose: purpose, Size: 1})
		require.Nil(t, apiErr, purpose)
		assert.Equal(t, "users/u9/"+folder, sig.Fields["folder"], purpose)
	}
}

func TestUploadSign_Refusals(t *testing.T) {
	cases := []struct {
		name string
		cfg  utils.UploadsConfig
		user string
		req  api.UploadSignRequest
		code int
	}{
		{"not configured", utils.UploadsConfig{CloudName: "demo", APIKey: "k"}, "u1", api.UploadSignRequest{Purpose: "profile", Size: 1}, http.StatusServiceUnavailable},
		{"no user", uploadCfg, "", api.UploadSignRequest{Purpose: "profile", Size: 1}, http.StatusUnauthorized},
		{"unknown purpose", uploadCfg, "u1", api.UploadSignRequest{Purpose: "../admin", Size: 1}, http.StatusBadRequest},
		{"empty purpose", uploadCfg, "u1", api.UploadSignRequest{Size: 1}, http.StatusBadRequest},
		{"zero size", uploadCfg, "u1", api.UploadSignRequest{Purpose: "profile"}, http.StatusBadRequest},
		{"too large", uploadCfg, "u1", api.UploadSignRequest{Purpose: "profile", Size: MaxUploadBytes + 1}, http.StatusBadRequest},
	}
	for _, c := range cases {
		q := &fakeUploadQuota{}
		sig, apiErr := newTestUploads(c.cfg, q).Sign(context.Background(), c.user, c.req)
		assert.Nil(t, sig, c.name)
		require.NotNil(t, apiErr, c.name)
		assert.Equal(t, c.code, apiErr.Code, c.name)
		assert.Empty(t, q.got, c.name+": no quota consumed")
	}
}

func TestUploadSign_QuotaReportMode(t *testing.T) {
	denied := &fakeUploadQuota{err: quotaExceeded(FeatureFileManager, LimitMaxUploads, 10, 10)}
	sig, apiErr := newTestUploads(uploadCfg, denied).Sign(context.Background(), "u1", api.UploadSignRequest{Purpose: "meals", Size: 10})
	require.Nil(t, apiErr, "report mode logs and allows")
	assert.NotEmpty(t, sig.Fields["signature"])

	down := &fakeUploadQuota{err: entitlementUnavailable(errors.New("mongo down"))}
	_, apiErr = newTestUploads(uploadCfg, down).Sign(context.Background(), "u1", api.UploadSignRequest{Purpose: "meals", Size: 10})
	assert.Nil(t, apiErr)
}

func TestUploadSign_QuotaEnforced(t *testing.T) {
	cfg := uploadCfg
	cfg.QuotaEnforce = true

	denied := &fakeUploadQuota{err: quotaExceeded(FeatureFileManager, LimitMaxUploads, 10, 10)}
	sig, apiErr := newTestUploads(cfg, denied).Sign(context.Background(), "u1", api.UploadSignRequest{Purpose: "meals", Size: 10})
	assert.Nil(t, sig)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)

	down := &fakeUploadQuota{err: entitlementUnavailable(errors.New("mongo down"))}
	_, apiErr = newTestUploads(cfg, down).Sign(context.Background(), "u1", api.UploadSignRequest{Purpose: "meals", Size: 10})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.Code)
}
