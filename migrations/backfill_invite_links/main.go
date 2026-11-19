// Command backfill_invite_links copies registration_links into hashed invite_links rows (kind client_personal) so
// legacy registration links keep working through the unified InviteLink aggregate. The registration_links collection
// is never modified or deleted: the raw-token fallback keeps working during and after the rollout.
//
// It is idempotent (a token whose hash already exists is skipped) and dry-runs by default.
//
// Usage: go run ./migrations/backfill_invite_links [--dry-run=false]
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

// migrationTimeout bounds the whole run, including the full-collection scan.
const migrationTimeout = 30 * time.Minute

func main() {
	dryRun := flag.Bool("dry-run", true, "only report the links that would be created")
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

	database := client.Database("users")
	src := repositories.NewRegistrationLinkRepository(database, "registration_links")
	dst := repositories.NewInviteLinkRepository(database)
	rep, err := services.RunInviteBackfill(ctx, src, dst, *dryRun, log.Printf)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}
	fmt.Printf("dry_run=%t legacy=%d existing=%d would_create=%d created=%d failed=%d\n",
		*dryRun, rep.Legacy, rep.Existing, rep.WouldCreate, rep.Created, rep.Failed)
}
