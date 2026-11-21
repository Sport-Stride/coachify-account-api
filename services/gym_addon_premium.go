package services

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
)

// premiumTimeout bounds the payments and users lookups of one premium annotation.
const premiumTimeout = 5 * time.Second

// PremiumInfo is the live coaching add-on state of one gym client.
type PremiumInfo struct {
	Since  *time.Time
	Addons []db.GymPremiumAddon
}

// PremiumAnnotator tells which clients of a gym hold a live coaching add-on. It never fails: a lookup problem is
// logged and the answer is nil (the lists then omit the premium fields).
type PremiumAnnotator interface {
	Lookup(ctx context.Context, gymID string, clientIDs []string) map[string]PremiumInfo
}

// AddonPremiumSource is the payments call behind PremiumAnnotator; *payments.PaymentClient satisfies it.
type AddonPremiumSource interface {
	GymAddonPremium(ctx context.Context, gymID string, clientIDs []string) ([]payments.GymAddonPremiumClient, error)
}

var _ AddonPremiumSource = (*payments.PaymentClient)(nil)

// AddonUserNames resolves users in bulk; *repositories.UserRepository satisfies it.
type AddonUserNames interface {
	GetUsersByExternalIds(ctx context.Context, externalIds []string) ([]db.UserResponse, *models.ApiError)
}

type gymAddonPremium struct {
	src   AddonPremiumSource
	users AddonUserNames
}

// NewPremiumAnnotator builds the payments-backed annotator (one payments call per gym per page, fail-open); a nil
// source gives nil.
func NewPremiumAnnotator(src AddonPremiumSource, users AddonUserNames) PremiumAnnotator {
	if src == nil {
		return nil
	}
	return &gymAddonPremium{src: src, users: users}
}

func (a *gymAddonPremium) Lookup(ctx context.Context, gymID string, clientIDs []string) map[string]PremiumInfo {
	if gymID == "" || len(clientIDs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, premiumTimeout)
	defer cancel()
	var clients []payments.GymAddonPremiumClient
	for start := 0; start < len(clientIDs); start += payments.MaxGymAddonPremiumIDs {
		end := start + payments.MaxGymAddonPremiumIDs
		if end > len(clientIDs) {
			end = len(clientIDs)
		}
		got, err := a.src.GymAddonPremium(ctx, gymID, clientIDs[start:end])
		if err != nil {
			zap.L().Warn("gym addon premium lookup failed, omitting premium fields", zap.String("component", "gym_addon"),
				zap.String("gym_id", gymID), zap.Error(err))
			return nil
		}
		clients = append(clients, got...)
	}
	names := a.coachNames(ctx, clients)
	out := make(map[string]PremiumInfo, len(clients))
	for _, c := range clients {
		if c.ClientID == "" || len(c.Addons) == 0 {
			continue
		}
		info := PremiumInfo{Since: c.Since}
		for _, ad := range c.Addons {
			info.Addons = append(info.Addons, db.GymPremiumAddon{
				PlanID: ad.PlanID, PlanName: ad.PlanName, CoachID: ad.CoachID, CoachName: names[ad.CoachID],
			})
		}
		out[c.ClientID] = info
	}
	return out
}

// coachNames maps the coach ids of the add-ons to display names; a lookup failure leaves the names empty.
func (a *gymAddonPremium) coachNames(ctx context.Context, clients []payments.GymAddonPremiumClient) map[string]string {
	seen := map[string]bool{}
	var ids []string
	for _, c := range clients {
		for _, ad := range c.Addons {
			if ad.CoachID != "" && !seen[ad.CoachID] {
				seen[ad.CoachID] = true
				ids = append(ids, ad.CoachID)
			}
		}
	}
	names := map[string]string{}
	if a.users == nil || len(ids) == 0 {
		return names
	}
	users, apiErr := a.users.GetUsersByExternalIds(ctx, ids)
	if apiErr != nil {
		zap.L().Warn("gym addon premium: coach names unavailable", zap.String("component", "gym_addon"), zap.Int("status", apiErr.Code))
		return names
	}
	for _, u := range users {
		names[u.ExternalID] = strings.TrimSpace(u.Firstname + " " + u.Lastname)
	}
	return names
}

// annotateGymClientRows sets the premium fields of the rows of one gym; a nil annotator or lookup leaves them out.
func annotateGymClientRows(ctx context.Context, p PremiumAnnotator, gymID string, rows []db.GymClientRow) {
	if p == nil || len(rows) == 0 {
		return
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ExternalID)
	}
	info := p.Lookup(ctx, gymID, ids)
	for i := range rows {
		if pi, ok := info[rows[i].ExternalID]; ok {
			rows[i].Premium, rows[i].PremiumSince, rows[i].PremiumAddons = true, pi.Since, pi.Addons
		}
	}
}
