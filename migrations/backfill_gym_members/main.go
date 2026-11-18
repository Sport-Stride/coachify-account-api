// Command backfill_gym_members reconciles gym_members with the legacy collections: gym owners from
// gyms.owner_id, head coaches from gym_head_coaches, coaches from coach_team_members whose head coach is in a gym
// (supervisor_id = the head coach). Every created row gets seat_assigned=false (grandfathered).
//
// A run applies, in this order: close active rows with no legacy counterpart, correct role/supervisor_id of rows
// that drifted, create missing rows. Closing first frees the one-gym-per-coach index for coaches that moved.
// It is idempotent (a row that already matches is never touched) and dry-runs by default; a dry run only reports
// what a real run would close, fix and create.
//
// Usage: go run ./migrations/backfill_gym_members [--dry-run=false]
// Env: MONGODB_URI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/utils"
)

// migrationTimeout bounds the whole run, including the full-collection scans (repositories apply no scan timeout).
const migrationTimeout = 30 * time.Minute

func main() {
	dryRun := flag.Bool("dry-run", true, "only report the rows that would be closed, fixed and created")
	flag.Parse()

	cfg := utils.LoadConfig()
	if cfg.MongoDB.URI == "" {
		log.Fatal("MONGODB_URI is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoDB.URI))
	if err != nil {
		log.Fatalf("connect to MongoDB: %v", err)
	}
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = client.Disconnect(dctx)
	}()

	repo := repositories.NewGymMemberRepository(client.Database("users"))

	// gym_members is read before the legacy snapshot: a row dual-written in between has a legacy counterpart
	// in the snapshot, so it can never look stale.
	existing, err := repo.ListActive(ctx)
	if err != nil {
		log.Fatalf("read gym_members: %v", err)
	}
	gyms, heads, team, err := repo.LegacySnapshot(ctx)
	if err != nil {
		log.Fatalf("read legacy collections: %v", err)
	}
	desired := services.BackfillMembers(gyms, heads, team)
	plan := services.ReconcileMembers(existing, desired)

	closed, fixed, created, failed := 0, 0, 0, 0
	if !*dryRun {
		for i := range plan.Close {
			r := &plan.Close[i]
			ok, err := repo.MarkLeft(ctx, r.GymID, r.UserID, time.Now())
			closed, failed = tally(ok, err, closed, failed, "close", r.GymID, r.UserID)
		}
		for i := range plan.Fix {
			r := &plan.Fix[i]
			ok, err := repo.FixRole(ctx, r)
			fixed, failed = tally(ok, err, fixed, failed, "fix", r.GymID, r.UserID)
		}
		for i := range plan.Create {
			r := &plan.Create[i]
			ok, err := repo.EnsureActive(ctx, r)
			created, failed = tally(ok, err, created, failed, "create", r.GymID, r.UserID)
		}
	}
	fmt.Printf("dry_run=%t gyms=%d heads=%d team=%d desired=%d existing_active=%d "+
		"would_close=%d would_fix=%d would_create=%d closed=%d fixed=%d created=%d failed=%d\n",
		*dryRun, len(gyms), len(heads), len(team), len(desired), len(existing),
		len(plan.Close), len(plan.Fix), len(plan.Create), closed, fixed, created, failed)
}

// tally counts one applied row or logs its failure (ids only).
func tally(ok bool, err error, done, failed int, op, gymID, userID string) (int, int) {
	switch {
	case err != nil:
		log.Printf("%s gym_id=%s user_id=%s: %v", op, gymID, userID, err)
		return done, failed + 1
	case ok:
		return done + 1, failed
	}
	return done, failed
}
