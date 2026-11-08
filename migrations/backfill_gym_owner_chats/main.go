// Command backfill_gym_owner_chats opens a chat conversation between every active gym owner and
// the gym's head coaches and team coaches. It is idempotent (chat-api get-or-creates) and
// dry-runs by default.
//
// Usage: go run ./migrations/backfill_gym_owner_chats [--dry-run=false] [--gym-id <id>]
// Env: MONGODB_URI, CHAT_API_URL, INTERNAL_API_KEY.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/pkg/chat"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/utils"
)

func main() {
	dryRun := flag.Bool("dry-run", true, "only count the conversations that would be ensured")
	gymID := flag.String("gym-id", "", "limit the run to one gym id")
	flag.Parse()

	cfg := utils.LoadConfig()
	if cfg.MongoDB.URI == "" {
		log.Fatal("MONGODB_URI is required")
	}
	if !*dryRun && cfg.ChatAPI.APIKey == "" {
		log.Fatal("INTERNAL_API_KEY is required unless --dry-run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoDB.URI))
	if err != nil {
		cancel()
		log.Fatalf("connect to MongoDB: %v", err)
	}
	cancel()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Disconnect(ctx)
	}()

	database := client.Database("users")
	teamRepo := repositories.NewTeamRepository(database, "users", "coach_clients")
	gymRepo := repositories.NewGymRepository(database, teamRepo)

	chatClient, err := chat.NewChatClient(cfg.ChatAPI)
	if err != nil {
		log.Fatalf("init chat client: %v", err)
	}
	p := services.NewGymChatProvisioner(services.NewGymChatStore(gymRepo, teamRepo), chatClient, func(f func()) { f() })

	pairs, created, failed := p.Backfill(context.Background(), *gymID, *dryRun)
	fmt.Printf("gym_id=%q dry_run=%t pairs=%d ensured=%d failed=%d\n", *gymID, *dryRun, pairs, created, failed)
}
