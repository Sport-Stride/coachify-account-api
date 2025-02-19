package app

import (
	"coachify-account-api/core"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"context"
	"fmt"
	"log"

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
	clientOptions := options.Client().ApplyURI(config.MongoDB.URI)
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	app.DB = client

	// Check the connection
	if err := app.DB.Ping(context.TODO(), nil); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connected to MongoDB!")

	// Initialize JWT middleware
	middleware := &jwt.GinJWTMiddleware{}

	pwChecker := core.NewPasswordChecker()

	// Initialize repositories
	db := client.Database("users")

	activationManager := core.NewSimpleActivationManager()
	identifier, err := identifier.NewIdentifierClient(config.IdentifierAPI)
	if err != nil {
		log.Fatalf("Failed to initialize identifier: %v", err)
	}
	coachRepo := repositories.NewCoachRepository(db, "coach_clients")
	userRepo := repositories.NewUserRepository(db, "users")
	notification := notification.NewNotificationClient(config.Notification.MailgunDomain,
		config.Notification.MailgunAPIKey)

	// Initialize OAuth2 providers
	facebookProvider, err := oauth2.NewProvider(
		oauth2.ProviderFacebook,
		config.FacebookOAuth.ClientID,
		config.FacebookOAuth.ClientSecret,
		config.FacebookOAuth.RedirectURL,
		config.FacebookEncryptionKey)
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
		providers,
	)
	// Initialize Router
	r := router.InitializeRouter(servicesWrapper)

	app.Config = *config
	app.Router = r

}

func (app *App) Run() {

	// Serving application

	port := app.Config.Port

	app.Router.Run(fmt.Sprintf(":%d", port))

}
func (app *App) Shutdown() {
	if err := app.DB.Disconnect(context.TODO()); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Déconnecté de MongoDB!")
}
