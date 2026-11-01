package api

import (
	"bytes"
	"encoding/json"
)

// OptBool is a JSON bool that distinguishes absent (Set=false), null (Set=true, Value=nil) and a value.
type OptBool struct {
	Set   bool
	Value *bool
}

func (o *OptBool) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v bool
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// FeaturePatch is one feature entry of an access update. Enabled null clears the override;
// a null limit clears that limit override.
type FeaturePatch struct {
	Enabled OptBool           `json:"enabled"`
	Limits  map[string]*int64 `json:"limits"`
}

// UpdateAccessRequest is the body of the PUT access endpoints (version is used for optimistic locking).
type UpdateAccessRequest struct {
	Features map[string]FeaturePatch `json:"features"`
	Reason   string                  `json:"reason,omitempty"`
	Version  *int64                  `json:"version,omitempty"`
}

// PlanDefaultRequest is the body of PUT /admin/entitlements/plan-defaults/:plan_id.
type PlanDefaultRequest struct {
	Features map[string]FeaturePatch `json:"features"`
	Reason   string                  `json:"reason,omitempty"`
}

// UsageDeltaRequest is the body of the internal consume and release endpoints.
type UsageDeltaRequest struct {
	Feature string           `json:"feature"`
	Deltas  map[string]int64 `json:"deltas"`
}

// ResolveRequest is the body of POST /internal/entitlements/resolve.
type ResolveRequest struct {
	UserIDs []string `json:"user_ids"`
}

// CheckQuotaRequest is the body of POST /internal/entitlements/:user_id/check-quota.
type CheckQuotaRequest struct {
	Feature string `json:"feature"`
	Limit   string `json:"limit"`
	Pending int64  `json:"pending"`
}
