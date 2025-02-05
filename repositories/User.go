package repositories

import (
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/models/masks"
	"context"
	"errors"
	"fmt"
	"log"

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
	// Create a unique index on the "email" field
	indexModel := mongo.IndexModel{
		Keys:    bson.M{"email": 1},              // Index on the "email" field
		Options: options.Index().SetUnique(true), // Ensure the index is unique
	}

	collection := client.Database(dbName).Collection(collName)
	// Create the index
	_, err := collection.Indexes().CreateOne(context.Background(), indexModel)
	if err != nil {
		log.Fatalf("Failed to create unique index on email: %v", err)
	}
	return &UserRepository{
		collection: collection}

}

// GetUserById retrieves a user by their ID
func (r *UserRepository) GetUserById(ctx context.Context, userId string) (*db.User, *models.ApiError) {
	var user db.User

	objID, err := primitive.ObjectIDFromHex(userId)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrInvalidIdFormat,
		}
	}

	err = r.collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: models.ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	return &user, nil
}

func (r *UserRepository) GetConfirmationDetails(ctx context.Context, email string) (*db.UserConfirmCode, bool, error) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"userConfirmCode":        1,
		"userVerificationStatus": 1,
	}

	// Execute the query
	var result struct {
		UserConfirmCode        *db.UserConfirmCode `bson:"userConfirmCode"`
		UserVerificationStatus bool                `bson:"userVerificationStatus"`
	}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, false, nil // User not found
		}
		return nil, false, fmt.Errorf("failed to fetch confirmation details: %w", err)
	}

	return result.UserConfirmCode, result.UserVerificationStatus, nil
}

func (r *UserRepository) UpdateConfirmationCode(ctx context.Context, email string, confirmCode *db.UserConfirmCode) error {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the new confirmation code and update the timestamp
	update := bson.M{
		"$set": bson.M{
			"userConfirmCode": confirmCode,
			"userUpdatedAt":   time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to update confirmation code: %w", err)
	}

	return nil
}

func (r *UserRepository) MarkUserAsVerified(ctx context.Context, email string) error {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the user as verified and active
	update := bson.M{
		"$set": bson.M{
			"userVerificationStatus": true,
			"userStatus":             "Active",
			"userUpdatedAt":          time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to mark user as verified: %w", err)
	}

	return nil
}

func (r *UserRepository) GetVerificationStatusAndID(ctx context.Context, email string) (bool, error) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"userVerificationStatus": 1,
		"_id":                    1,
	}

	// Execute the query
	var result struct {
		UserVerificationStatus bool `bson:"userVerificationStatus"`
	}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, models.ErrUserNotFound // User not found
		}
		return false, models.ErrFailedToFetchVerifStatus
	}

	return result.UserVerificationStatus, nil
}

func (r *UserRepository) GetStatusAndID(ctx context.Context, email string) (db.UserStatus, error) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"userStatus": 1,
	}

	// Execute the query
	var result struct {
		UserStatus db.UserStatus `bson:"userStatus"`
	}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", models.ErrUserNotFound // User not found
		}
		return "", models.ErrFailedToFetchUserStatus
	}

	return result.UserStatus, nil
}

func (r *UserRepository) UpdateResetPasswordCode(ctx context.Context, email string, resetCode *db.UserResetPasswordCode) error {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the new reset password code and update the timestamp
	update := bson.M{
		"$set": bson.M{
			"userResetPasswordCode": resetCode,
			"userUpdatedAt":         time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return models.ErrUpdateResetPassword
	}

	return nil
}

func (r *UserRepository) GetStatusAndResetPasswordCode(ctx context.Context, email string) (db.UserStatus, *db.UserResetPasswordCode, error) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"userStatus":            1,
		"userResetPasswordCode": 1,
	}

	// Execute the query
	var result struct {
		UserStatus            db.UserStatus             `bson:"userStatus"`
		UserResetPasswordCode *db.UserResetPasswordCode `bson:"userResetPasswordCode"`
	}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", nil, models.ErrUserNotFound // User not found
		}
		return "", nil, models.ErrFailedToFetchUserStatus
	}

	return result.UserStatus, result.UserResetPasswordCode, nil
}

func (r *UserRepository) UpdatePasswordAndClearResetCode(ctx context.Context, email, encryptedPassword string) error {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the new password and clear the reset password code
	update := bson.M{
		"$set": bson.M{
			"userPassword":          encryptedPassword,
			"userResetPasswordCode": nil,
			"userUpdatedAt":         time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return models.ErrInternalError
	}

	return nil
}

// CreateUser creates a new user in the database
func (r *UserRepository) CreateUser(ctx context.Context, dbUser *db.User) (*db.UserResponse, *models.ApiError) {
	// Generate a new ObjectID for the user
	dbUser.ID = primitive.NewObjectID()

	// Insert the user into the database
	_, err := r.collection.InsertOne(ctx, dbUser)
	if err != nil {
		// Check if the error is a duplicate key error (email already exists)
		if mongo.IsDuplicateKeyError(err) {
			return nil, &models.ApiError{
				Code:  http.StatusConflict,
				Error: models.ErrEmailAlreadyExists,
			}
		}

		// Log the error for debugging purposes
		fmt.Printf("Failed to create user: %v\n", err)

		// Return a generic internal server error
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToCreateUser,
		}
	}

	// Map the database user to the response model
	userResponse := mapping.ToUserResponse(dbUser)

	return &userResponse, nil
}

func (r *UserRepository) GetUsersByOrgID(ctx context.Context, orgID string) (int, *models.ApiError) {
	filter := bson.M{"organization_id": orgID}

	count, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
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
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx) // Always close the cursor to avoid leaks

	// Count the total number of documents matching the filters
	count, err := col.CountDocuments(ctx, filters)
	if err != nil {
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Iterate through the results and decode them
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			return nil, 0, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrFailedDecodeUser,
			}

		}

		// Prepare the response with user and role details
		userResponse := mapping.ToUserResponse(&user)

		results = append(results, &userResponse)
	}

	// Handle potential cursor errors
	if err := cursor.Err(); err != nil {
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
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
				Error: models.ErrUserNotFound,
			}
		}
		utils.Logger.Error("Error retrieving user from the database", zap.String("email", email), zap.Error(err))

		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
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
			Error: models.ErrUpdateUser,
		}
	}

	if result.MatchedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
	}

	if result.ModifiedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotModified,
			Error: models.ErrNoChangesToUser,
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
				Error: models.ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
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
				Error: models.ErrUserNotFound,
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Prepare the response with user and role details
	userResponse := mapping.ToUserResponse(&user)

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
			Code:  http.StatusInternalServerError,
			Error: models.ErrUpdateUser,
		}
	}

	// If no document was matched and updated, return a custom "Not Found" error
	if result.MatchedCount == 0 {
		return nil, &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
	}

	// Prepare the response with user and role details
	userResponse := mapping.ToUserResponse(user)

	// Return the updated user struct
	return &userResponse, nil
}

// DeleteUser deletes a user by externalID from the database.
func (r *UserRepository) DeleteUser(ctx context.Context, externalID string) *models.ApiError {

	// Attempt to delete the user with the specified externalID.
	result, err := r.collection.DeleteOne(ctx, bson.M{"externalid": externalID})
	if err != nil {
		// Return a 500 Internal Server Error if the deletion operation fails.
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrUserDeletionFailed,
		}
	}

	// Check if no documents were deleted, meaning the user was not found.
	if result.DeletedCount == 0 {
		// Return a 404 Not Found error if the user does not exist.
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
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
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
		}
	}
	defer cursor.Close(ctx) // Always close the cursor to avoid leaks

	// Iterate through the results and decode them
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrFailedDecodeUser,
			}
		}

		// Prepare the response with user details
		userResponse := mapping.ToUserResponse(&user)

		// Add the user response to the results
		results = append(results, &userResponse)
	}

	// Handle potential cursor errors
	if err := cursor.Err(); err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Return the results
	return results, nil
}
