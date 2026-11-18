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
	rep, err := services.RunMemberBackfill(ctx, repo, *dryRun, log.Printf)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}
	fmt.Printf("dry_run=%t gyms=%d heads=%d team=%d desired=%d existing_active=%d "+
		"would_close=%d would_fix=%d would_create=%d closed=%d fixed=%d created=%d failed=%d\n",
		*dryRun, rep.Gyms, rep.Heads, rep.Team, rep.Desired, rep.ExistingActive,
		rep.WouldClose, rep.WouldFix, rep.WouldCreate, rep.Closed, rep.Fixed, rep.Created, rep.Failed)
}
