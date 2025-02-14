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
	clientOptions := options.Client().ApplyURI(config.MongoDB.MongoURI)
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

	activationManager := core.NewSimpleActivationManager()
	identifier, err := identifier.NewIdentifierClient(config.IdentifierAPI)
	userRepo := repositories.NewUserRepository(app.DB, "users", "users")
	notification := notification.NewNotificationClient(config.Notification.MailgunDomain,
		config.Notification.MailgunAPIKey)

	// Initialize OAuth2 providers
	facebookProvider, err := oauth2.NewProvider(oauth2.ProviderFacebook, config.FacebookAppID, config.FacebookAppSecret, config.FacebookRedirectURL)
	if err != nil {
		log.Fatalf("Failed to initialize Facebook provider: %v", err)
	}

	googleProvider, err := oauth2.NewProvider(oauth2.ProviderGoogle, config.GoogleAppID, config.GoogleAppSecret, config.GoogleRedirectURL)
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
		config,
		middleware,
		*userRepo,
		pwChecker,
		activationManager,
		identifier,
		notification,
		providers,
	)
	// Initialize Router
	r := router.InitializeRouter(servicesWrapper)

	app.Config = config
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
