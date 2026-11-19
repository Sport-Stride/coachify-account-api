package repositories

import (
	"context"

	"coachify-account-api/models/db"
)

// PersonalClientsOfCoach returns the coach's OWN personal clients (coach_clients, safe fields only). It exists for the
// coach's own scope and must never be called with another user's id: personal clients are visible to that coach alone.
func (r *AnalyticsRepository) PersonalClientsOfCoach(ctx context.Context, coachID string, f db.ScopeClientFilter) ([]db.ClientRowLinked, int, error) {
	if coachID == "" {
		return []db.ClientRowLinked{}, 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	return runPaged[db.ClientRowLinked](ctx, r.team.clients, clientRowsPipeline(r.team.usersName, bd("coach_id", coachID), "$created_at", f))
}
