package services

import (
	"context"
	"crypto/sha1" //nolint:gosec // Cloudinary upload signatures are SHA-1 by default
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/utils"
)

// UploadPurposes maps an upload purpose to its folder under users/<external_id>/. Only add entries.
var UploadPurposes = map[string]string{
	"profile":  "profile",
	"progress": "progress",
	"meals":    "meals",
	"library":  "library",
}

const (
	// MaxUploadBytes bounds the declared file size (Cloudinary's own per-file limit still applies).
	MaxUploadBytes = 20 << 20
	// uploadSignatureTTL is how long Cloudinary accepts a signed timestamp.
	uploadSignatureTTL = time.Hour
	// uploadFormats and uploadTransformation are signed, so the client cannot change them: images only,
	// stored at most 2048 px on the long side (the web route resizes to the same bound).
	uploadFormats        = "jpg,jpeg,png,webp,heic,heif"
	uploadTransformation = "c_limit,h_2048,w_2048"
)

// UploadService signs direct image uploads to Cloudinary.
type UploadService interface {
	Sign(ctx context.Context, userID string, req api.UploadSignRequest) (*api.UploadSignature, *models.ApiError)
}

// UploadQuota consumes the file_manager quota; EntitlementService satisfies it.
type UploadQuota interface {
	Consume(ctx context.Context, userID string, req api.UsageDeltaRequest) (map[string]int64, *models.ApiError)
}

type uploadService struct {
	cfg   utils.UploadsConfig
	quota UploadQuota
	now   func() time.Time
}

// NewUploadService builds the upload signer; quota may be nil (no quota check).
func NewUploadService(cfg utils.UploadsConfig, quota UploadQuota) UploadService {
	return &uploadService{cfg: cfg, quota: quota, now: time.Now}
}

var (
	errUploadsDisabled = &models.ApiError{Code: http.StatusServiceUnavailable, Error: errors.New("uploads not configured")}
	errUploadPurpose   = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid purpose: expected profile, progress, meals or library")}
	errUploadSize      = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid size: expected 1 byte to 20 MB")}
)

// Sign returns the fields of a signed upload into users/<userID>/<folder>. The folder, the accepted formats and the
// resize are part of the signature, so a client can only upload images into its own folder. One upload of req.Size
// bytes is counted against the file_manager quota before signing.
func (s *uploadService) Sign(ctx context.Context, userID string, req api.UploadSignRequest) (*api.UploadSignature, *models.ApiError) {
	if !s.cfg.Enabled() {
		return nil, errUploadsDisabled
	}
	if userID == "" {
		return nil, &models.ApiError{Code: http.StatusUnauthorized, Error: errors.New("user not authenticated")}
	}
	folder, ok := UploadPurposes[req.Purpose]
	if !ok {
		return nil, errUploadPurpose
	}
	if req.Size <= 0 || req.Size > MaxUploadBytes {
		return nil, errUploadSize
	}
	if apiErr := s.consumeQuota(ctx, userID, req.Size); apiErr != nil {
		return nil, apiErr
	}

	now := s.now()
	params := map[string]string{
		"allowed_formats": uploadFormats,
		"folder":          "users/" + userID + "/" + folder,
		"timestamp":       strconv.FormatInt(now.Unix(), 10),
		"transformation":  uploadTransformation,
	}
	fields := make(map[string]string, len(params)+2)
	for k, v := range params {
		fields[k] = v
	}
	fields["api_key"] = s.cfg.APIKey
	fields["signature"] = cloudinarySignature(params, s.cfg.APISecret)
	return &api.UploadSignature{
		UploadURL: "https://api.cloudinary.com/v1_1/" + s.cfg.CloudName + "/image/upload",
		Fields:    fields,
		ExpiresAt: now.Add(uploadSignatureTTL).Unix(),
	}, nil
}

// consumeQuota counts one upload. Outside enforce mode a denial or an outage is logged and the upload allowed.
func (s *uploadService) consumeQuota(ctx context.Context, userID string, size int64) *models.ApiError {
	if s.quota == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, apiErr := s.quota.Consume(ctx, userID, api.UsageDeltaRequest{
		Feature: FeatureFileManager,
		Deltas:  map[string]int64{"uploads": 1, "bytes": size},
	})
	if apiErr == nil {
		return nil
	}
	if s.cfg.QuotaEnforce {
		return apiErr
	}
	zap.L().Warn("upload quota not applied (report mode)",
		zap.String("component", "uploads"),
		zap.String("user_id", userID),
		zap.Int("status", apiErr.Code),
	)
	return nil
}

// cloudinarySignature signs upload parameters the Cloudinary way: the parameters sorted by name as
// "name=value" joined with "&", the API secret appended, SHA-1 in hex.
func cloudinarySignature(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "&") + secret)) //nolint:gosec // see import
	return hex.EncodeToString(sum[:])
}
