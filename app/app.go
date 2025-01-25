package app

import (
	"context"
	"fmt"
	"log"
	"mbv-common-template-api/core"
	"mbv-common-template-api/pkg/identifier"
	"mbv-common-template-api/pkg/notification"
	"mbv-common-template-api/repositories"

	jwt "github.com/appleboy/gin-jwt/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"mbv-common-template-api/router"
	"mbv-common-template-api/services"
	"mbv-common-template-api/utils"

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
	notification := notification.NewNotificationClient(config.Notification)

	// Initialize Services
	servicesWrapper := services.InitServices(
		config,
		middleware,
		*userRepo,
		pwChecker,
		activationManager,
		identifier,
		notification,
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
