package app

import (
	"coachify-account-api/core"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
	"context"
	"fmt"
	"log"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/router"
	"coachify-account-api/services"
	"coachify-account-api/utils"

	"github.com/gin-gonic/gin"
)

type App struct {
	Config utils.AppConfig
	Router *gin.Engine
	DB     *mongo.Client
}

func New() *App {
	app := &App{}
	app.setup()
	return app
}

func (app *App) setup() {
	// Load configuration
	config := utils.LoadConfig()

	// Establish connection to MongoDB
	log.Println("Connecting to MongoDB...")
	clientOptions := options.Client().ApplyURI(config.MongoDB.URI)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	app.DB = client

	// Check the connection
	if err := app.DB.Ping(ctx, nil); err != nil {
		log.Fatalf("Failed to ping MongoDB: %v", err)
	}
	log.Println("Successfully connected to MongoDB!")

	// Get database
	db := client.Database("users")

	// Initialize indexes (CRITICAL for performance)
	log.Println("Initializing database indexes...")
	indexCtx, indexCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer indexCancel()
	
	if err := repositories.InitializeIndexes(indexCtx, db); err != nil {
		// In production, you might want to fail here
		// For now, we'll log and continue
		log.Printf("WARNING: Failed to initialize indexes: %v", err)
		log.Println("Application will continue, but performance may be degraded")
	} else {
		log.Println("Database indexes initialized successfully")
	}

	// Initialize JWT middleware
	middleware := &jwt.GinJWTMiddleware{}

	pwChecker := core.NewPasswordChecker()

	// Initialize activation manager and clients
	activationManager := core.NewSimpleActivationManager()
	
	identifier, err := identifier.NewIdentifierClient(config.IdentifierAPI)
	if err != nil {
		log.Fatalf("Failed to initialize identifier: %v", err)
	}
	
	invitation, err := invitation.NewInvitationClient(config.InvitationAPI)
	if err != nil {
		log.Fatalf("Failed to initialize invitation: %v", err)
	}
	
	payment, err := payments.NewPaymentClient(config.PaymentAPI)
	if err != nil {
		log.Fatalf("Failed to initialize payment client: %v", err)
	}
	
	notification, err := notification.NewNotificationClient(config.NotificationAPI)
	if err != nil {
		log.Fatalf("Failed to initialize notification client: %v", err)
	}

	// Initialize repositories
	userColl := db.Collection("users")
	userRepo := repositories.NewUserRepository(userColl)
	coachRepo := repositories.NewCoachRepository(db, "coach_clients", userColl)

	// Initialize OAuth2 providers
	facebookProvider, err := oauth2.NewProvider(
		oauth2.ProviderFacebook,
		config.FacebookOAuth.ClientID,
		config.FacebookOAuth.ClientSecret,
		config.FacebookOAuth.RedirectURL,
		config.FacebookEncryptionKey,
	)
	if err != nil {
		log.Fatalf("Failed to initialize Facebook provider: %v", err)
	}

	googleProvider, err := oauth2.NewProvider(
		oauth2.ProviderGoogle,
		config.GoogleOAuth.ClientID,
		config.GoogleOAuth.ClientSecret,
		config.GoogleOAuth.RedirectURL,
		config.GoogleEncryptionKey,
	)
	if err != nil {
		log.Fatalf("Failed to initialize Google provider: %v", err)
	}

	// Create a map of providers
	providers := map[oauth2.ProviderType]oauth2.Provider{
		oauth2.ProviderFacebook: facebookProvider,
		oauth2.ProviderGoogle:   googleProvider,
	}

	// Initialize Services
	servicesWrapper := services.InitServices(
		*config,
		middleware,
		userRepo,
		coachRepo,
		pwChecker,
		activationManager,
		identifier,
		notification,
		invitation,
		providers,
		payment,
	)
	
	// Initialize Router
	r := router.InitializeRouter(servicesWrapper)

	app.Config = *config
	app.Router = r
}

func (app *App) Run() {
	port := app.Config.Port
	log.Printf("🚀 Server starting on port %d (environment: %s)", port, app.Config.Environment)
	
	if err := app.Router.Run(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func (app *App) Shutdown() {
	log.Println("Shutting down application...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	if err := app.DB.Disconnect(ctx); err != nil {
		log.Printf("Error disconnecting from MongoDB: %v", err)
	} else {
		log.Println("Successfully disconnected from MongoDB")
	}
}