package router

import (
	"context"

	"coachify-account-api/models/db"
)

func (g *StatusGate) SetMaxEntriesForTest(n int) { g.maxEntries = n }
func (g *StatusGate) Status(ctx context.Context, id string) (db.UserStatus, error) {
	return g.status(ctx, id)
}
