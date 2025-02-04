package repositories

import (
	"coachify-account-api/models/db"
	"coachify-account-api/models/masks"
	"context"
	"errors"
	"fmt"

	"net/http"
	"strconv"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type UserRepository struct {
	collection *mongo.Collection
}

func NewUserRepository(client *mongo.Client, dbName, collName string) *UserRepository {
	collection := client.Database(dbName).Collection(collName)
	return &UserRepository{
		collection: collection}

}

var (
	ErrUserNotFound    = errors.New("user not found")
	EmailAlreadyExists = errors.New("email already exists")
	FailedToCreateUser = errors.New("failed to create user")
)

// GetUserById retrieves a user by their ID
func (r *UserRepository) GetUserById(ctx context.Context, userId string) (*db.User, *models.ApiError) {
	var user db.User

	objID, err := primitive.ObjectIDFromHex(userId)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: fmt.Errorf("invalid user ID format: %w", err),
		}
	}

	err = r.collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: err,
		}
	}

	return &user, nil
}

// CreateUser creates a new user in the database
func (r *UserRepository) CreateUser(ctx context.Context, dbUser *db.User) (*db.UserResponse, *models.ApiError) {

	dbUser.ID = primitive.NewObjectID()

	// Check if the email is already in use
	filter := bson.M{"email": dbUser.UserEmail}
	var existingUser db.User
	err := r.collection.FindOne(ctx, filter).Decode(&existingUser)
	if err == nil {
		// If a user with this email already exists, return an error
		return nil, &models.ApiError{
			Code:  http.StatusConflict,
			Error: EmailAlreadyExists,
		}
	}

	_, err = r.collection.InsertOne(ctx, dbUser)

	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: FailedToCreateUser,
		}
	}

	// Prepare the response with user and role details
	userResponse := db.UserResponse{
		Id:                 dbUser.ID,
		Email:              dbUser.UserEmail,
		Firstname:          dbUser.UserFirstname,
		Lastname:           dbUser.UserLastname,
		Password:           dbUser.UserPassword,
		Gender:             string(dbUser.UserGender),
		Status:             string(dbUser.UserStatus),
		ProfilePicture:     dbUser.UserProfilePicture,
		Description:        dbUser.UserDescription,
		PhoneNumber:        dbUser.UserPhoneNumber,
		Address:            dbUser.UserAddress,
		VerificationStatus: dbUser.UserVerificationStatus,
		CreatedAt:          dbUser.UserCreatedAt,
		UpdatedAt:          dbUser.UserUpdatedAt,
		LastLogin:          dbUser.UserLastLogin,
		ExternalID:         dbUser.ExternalID,
		Token:              dbUser.Token,
		RefreshToken:       dbUser.UserRefreshToken,
		Role:               dbUser.UserRole,
		Autologin:          dbUser.Autologin,
	}

	return &userResponse, nil

}
func (r *UserRepository) GetUsersByOrgID(ctx context.Context, orgID string) (int, *models.ApiError) {
	filter := bson.M{"organization_id": orgID}

	count, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: err,
		}
	}

	return int(count), nil
}

func (r *UserRepository) GetAllUsersPag(ctx context.Context, s *db.SearchUser) ([]*db.UserResponse, int, *models.ApiError) {
	// Initialize the results slice
	results := make([]*db.UserResponse, 0)

	// Use the collection defined in the repository
	col := r.collection

	// Convert page and size to integers with default values
	page, err := strconv.Atoi(s.Page)
	if err != nil || page < 1 {
		page = 1
	}

	size, err := strconv.Atoi(s.Size)
	if err != nil || size < 1 {
		size = 3 // Default value for size
	}

	// Use the SearchUserMasks function to generate dynamic filters
	filters := masks.SearchUserMasks(s) // Assuming a similar function exists for users

	// Define pagination options
	opts := options.Find().
		SetLimit(int64(size)).
		SetSkip(int64((page - 1) * size))

	// Execute the query with filters and pagination options
	cursor, err := col.Find(ctx, filters, opts)
	if err != nil {
		return nil, 0, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("failed to retrieve users: %w", err),
		}
	}
	defer cursor.Close(ctx) // Always close the cursor to avoid leaks

	// Count the total number of documents matching the filters
	count, err := col.CountDocuments(ctx, filters)
	if err != nil {
		return nil, 0, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("failed to count users: %w", err),
		}
	}

	// Iterate through the results and decode them
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			return nil, 0, &models.ApiError{
				Code:  500,
				Error: fmt.Errorf("failed to decode user: %w", err),
			}

		}

		// Prepare the response with user and role details
		userResponse := db.UserResponse{
			Id:                 user.ID,
			Email:              user.UserEmail,
			Firstname:          user.UserFirstname,
			Lastname:           user.UserLastname,
			Password:           user.UserPassword,
			Gender:             string(user.UserGender),
			Status:             string(user.UserStatus),
			ProfilePicture:     user.UserProfilePicture,
			Description:        user.UserDescription,
			PhoneNumber:        user.UserPhoneNumber,
			Address:            user.UserAddress,
			VerificationStatus: user.UserVerificationStatus,
			CreatedAt:          user.UserCreatedAt,
			UpdatedAt:          user.UserUpdatedAt,
			LastLogin:          user.UserLastLogin,
			ExternalID:         user.ExternalID,
			Token:              user.Token,
			RefreshToken:       user.UserRefreshToken,
			Role:               user.UserRole,
			Autologin:          user.Autologin,
		}

		results = append(results, &userResponse)
	}

	// Handle potential cursor errors
	if err := cursor.Err(); err != nil {
		return nil, 0, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("cursor error: %w", err),
		}
	}

	// Return the results and the total number of documents
	return results, int(count), nil
}

// GetByEmail retrieves a user by email
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*db.User, *models.ApiError) {
	var user db.User

	filter := bson.M{"email": email}

	err := r.collection.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: fmt.Errorf("user with email %s not found", email),
			}
		}
		utils.Logger.Error("Error retrieving user from the database", zap.String("email", email), zap.Error(err))

		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: fmt.Errorf("error retrieving user from the database: %w", err),
		}
	}

	return &user, nil
}

// Update updates user information in the database
func (r *UserRepository) Update(ctx context.Context, id primitive.ObjectID, user *db.User) *models.ApiError {

	filter := bson.M{"_id": id}

	update := bson.M{
		"$set": bson.M{
			"confirm_code":        user.UserConfirmCode,
			"status":              user.UserStatus,
			"updated_at":          time.Now(),
			"reset_password_code": user.UserResetPasswordCode,
			"verification_status": user.UserVerificationStatus,
			"password":            user.UserPassword,
			"last_login":          user.UserLastLogin,
			"autologin":           user.Autologin,
			"token":               user.Token,
			"refresh_token":       user.UserRefreshToken,
		},
	}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: fmt.Errorf("failed to update user: %w", err),
		}
	}

	if result.MatchedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: fmt.Errorf("no user found with ID: %v", id),
		}
	}

	if result.ModifiedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotModified,
			Error: fmt.Errorf("no changes made to user with ID: %v", id),
		}
	}

	return nil
}

// GetUserByExternalId retrieves a user by their external_id
func (r *UserRepository) GetUserByExternalIdUpdate(ctx context.Context, userId string) (*db.User, *models.ApiError) {

	var user db.User

	err := r.collection.FindOne(ctx, bson.M{"externalid": userId}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: err,
		}
	}

	return &user, nil

}

// GetUserByExternalId retrieves a user by their external_id
func (r *UserRepository) GetUserByExternalId(ctx context.Context, userId string) (*db.UserResponse, *models.ApiError) {

	var user db.User

	err := r.collection.FindOne(ctx, bson.M{"externalid": userId}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: err,
		}
	}

	// Prepare the response with user and role details
	userResponse := db.UserResponse{
		Id:                 user.ID,
		Email:              user.UserEmail,
		Firstname:          user.UserFirstname,
		Lastname:           user.UserLastname,
		Password:           user.UserPassword,
		Gender:             string(user.UserGender),
		Status:             string(user.UserStatus),
		ProfilePicture:     user.UserProfilePicture,
		Description:        user.UserDescription,
		PhoneNumber:        user.UserPhoneNumber,
		Address:            user.UserAddress,
		VerificationStatus: user.UserVerificationStatus,
		CreatedAt:          user.UserCreatedAt,
		UpdatedAt:          user.UserUpdatedAt,
		LastLogin:          user.UserLastLogin,
		ExternalID:         user.ExternalID,
		Token:              user.Token,
		RefreshToken:       user.UserRefreshToken,
		Role:               user.UserRole,
		Autologin:          user.Autologin,
	}

	return &userResponse, nil

}

// UpdateUser updates an existing user in the MongoDB collection.
func (r *UserRepository) UpdateUser(ctx context.Context, user *db.User) (*db.UserResponse, *models.ApiError) {
	// MongoDB filter to find the user by user_id
	filter := bson.M{"externalid": user.ExternalID}

	// Ensure the CreatedAt field is set if not already provided
	if user.UserCreatedAt.IsZero() {
		now := time.Now()
		user.UserCreatedAt = now
	}

	// Always update the UpdatedAt field
	user.UserUpdatedAt = time.Now()

	// Replace the document in MongoDB that matches the filter with the new user data
	result, err := r.collection.ReplaceOne(ctx, filter, user)
	if err != nil {
		// Return an API error if something goes wrong
		return nil, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("Failed to update user"),
		}
	}

	// If no document was matched and updated, return a custom "Not Found" error
	if result.MatchedCount == 0 {
		return nil, &models.ApiError{
			Code:  404,
			Error: fmt.Errorf("user not found"),
		}
	}

	// Prepare the response with user and role details
	userResponse := db.UserResponse{
		Id:                 user.ID,
		Email:              user.UserEmail,
		Firstname:          user.UserFirstname,
		Lastname:           user.UserLastname,
		Password:           user.UserPassword,
		Gender:             string(user.UserGender),
		Status:             string(user.UserStatus),
		ProfilePicture:     user.UserProfilePicture,
		Description:        user.UserDescription,
		PhoneNumber:        user.UserPhoneNumber,
		Address:            user.UserAddress,
		VerificationStatus: user.UserVerificationStatus,
		CreatedAt:          user.UserCreatedAt,
		UpdatedAt:          user.UserUpdatedAt,
		LastLogin:          user.UserLastLogin,
		ExternalID:         user.ExternalID,
		Token:              user.Token,
		RefreshToken:       user.UserRefreshToken,
		Role:               user.UserRole,
		Autologin:          user.Autologin,
	}

	// Return the updated user struct
	return &userResponse, nil
}

// DeleteUser deletes a user by externalID from the database.
func (r *UserRepository) DeleteUser(ctx context.Context, externalID string) *models.ApiError {
	// Set a timeout of 5 seconds for the operation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() // Ensure the context is canceled after the operation.

	// Attempt to delete the user with the specified externalID.
	result, err := r.collection.DeleteOne(ctx, bson.M{"externalid": externalID})
	if err != nil {
		// Return a 500 Internal Server Error if the deletion operation fails.
		return &models.ApiError{
			Code:  500,
			Error: errors.New("failed to delete user"),
		}
	}

	// Check if no documents were deleted, meaning the user was not found.
	if result.DeletedCount == 0 {
		// Return a 404 Not Found error if the user does not exist.
		return &models.ApiError{
			Code:  404,
			Error: errors.New("user not found"),
		}
	}

	// Return nil to indicate the user was successfully deleted.
	return nil
}
func (r *UserRepository) GetAllUsers(ctx context.Context) ([]*db.UserResponse, *models.ApiError) {
	// Initialize the results slice
	results := make([]*db.UserResponse, 0)

	// Use the collection defined in the repository
	col := r.collection

	// Execute the query without filters (retrieving all users)
	cursor, err := col.Find(ctx, bson.D{}) // Empty filter to get all users
	if err != nil {
		return nil, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("failed to retrieve users: %w", err),
		}
	}
	defer cursor.Close(ctx) // Always close the cursor to avoid leaks

	// Iterate through the results and decode them
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			return nil, &models.ApiError{
				Code:  500,
				Error: fmt.Errorf("failed to decode user: %w", err),
			}
		}

		// Prepare the response with user details
		userResponse := db.UserResponse{
			Id:                 user.ID,
			Email:              user.UserEmail,
			Firstname:          user.UserFirstname,
			Lastname:           user.UserLastname,
			Password:           user.UserPassword,
			Gender:             string(user.UserGender),
			Status:             string(user.UserStatus),
			ProfilePicture:     user.UserProfilePicture,
			Description:        user.UserDescription,
			PhoneNumber:        user.UserPhoneNumber,
			Address:            user.UserAddress,
			VerificationStatus: user.UserVerificationStatus,
			CreatedAt:          user.UserCreatedAt,
			UpdatedAt:          user.UserUpdatedAt,
			LastLogin:          user.UserLastLogin,
			ExternalID:         user.ExternalID,
			Token:              user.Token,
			RefreshToken:       user.UserRefreshToken,
			Role:               user.UserRole,
			Autologin:          user.Autologin,
		}

		// Add the user response to the results
		results = append(results, &userResponse)
	}

	// Handle potential cursor errors
	if err := cursor.Err(); err != nil {
		return nil, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("cursor error: %w", err),
		}
	}

	// Return the results
	return results, nil
}
