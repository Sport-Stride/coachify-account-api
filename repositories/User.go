package repositories

import (
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/models/masks"
	"context"
	"errors"
	"fmt"

	"net/http"
	"regexp"
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

func NewUserRepository(userColl *mongo.Collection) *UserRepository {
	// collection := db.Collection(collName)
	// indexModel := mongo.IndexModel{
	// 	Keys:    bson.M{"email": 1},              // index key
	// 	Options: options.Index().SetUnique(true), // unique index option
	// }

	// // Use a context with a timeout to avoid hanging indefinitely
	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// defer cancel()

	// indexName, err := collection.Indexes().CreateOne(ctx, indexModel)
	// if err != nil {
	// 	return nil
	// }
	// fmt.Printf("Created index %s for collection %s\n", indexName, collName)

	return &UserRepository{collection: userColl}
}

func (r *UserRepository) CreateUsers(ctx context.Context, users []*db.User) ([]*db.User, error) {
	if len(users) == 0 {
		return nil, nil
	}

	// Convert users to a slice of interface{} for bulk insertion
	var documents []interface{}
	for _, user := range users {
		documents = append(documents, user)
	}

	// Perform bulk insert
	opts := options.InsertMany().SetOrdered(false) // SetOrdered(false) allows continuing on errors
	result, err := r.collection.InsertMany(ctx, documents, opts)
	if err != nil {
		if mongoErr, ok := err.(mongo.BulkWriteException); ok {
			for _, writeErr := range mongoErr.WriteErrors {
				zap.L().Error("bulk insert user failed",
					zap.String("component", "repository"),
					zap.Any("error", writeErr),
				)
			}
		}
		return nil, fmt.Errorf("failed to insert users: %w", err)
	}

	// Map inserted IDs back to users
	for i, insertID := range result.InsertedIDs {
		users[i].ID = insertID.(primitive.ObjectID) // Assuming ID is of type primitive.ObjectID
	}

	return users, nil
}

// GetUserNameByExternalID retrieves only the first and last name of a user using their external ID.
func (r *UserRepository) GetUserNameByExternalID(ctx context.Context, externalID string) (string, *models.ApiError) {
	// Create a filter to search by externalID.
	filter := bson.M{"externalid": externalID}
	// Use projection to retrieve only the firstname and lastname fields.
	opts := options.FindOne()

	// Define a temporary struct to hold the result.
	var result struct {
		Firstname string `bson:"firstname"`
		Lastname  string `bson:"lastname"`
	}

	// Query with projection.
	err := r.collection.FindOne(ctx, filter, opts).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			utils.Logger.Info("No user found with externalID", zap.String("externalID", externalID))
			return "", &models.ApiError{
				Code:  http.StatusNotFound,
				Error: models.ErrUserNotFound,
			}
		}
		utils.Logger.Error("Error retrieving user by externalID", zap.Error(err))
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Concatenate first name and last name to form the full name.
	fullName := fmt.Sprintf("%s %s", result.Firstname, result.Lastname)
	return fullName, nil
}

// GetUserById retrieves a user by their ID
func (r *UserRepository) GetUserById(ctx context.Context, userId string) (*db.User, *models.ApiError) {
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

func (r *UserRepository) UpdateConfirmationCode(ctx context.Context, user *api.GetUserConfirm) *models.ApiError {
	// Define a filter to find the user by email
	filter := bson.M{"email": user.UserEmail}

	// Define the update to set the new confirmation code and update the timestamp
	update := bson.M{
		"$set": bson.M{
			"status":              user.UserStatus,
			"verification_status": user.UserVerificationStatus,
			"confirm_code":        user.UserConfirmCode,
			"updated_at":          time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
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

func (r *UserRepository) UpdateResetPasswordCode(ctx context.Context, email string, resetCode *db.UserResetPasswordCode) *models.ApiError {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the new reset password code and update the timestamp
	update := bson.M{
		"$set": bson.M{
			"reset_password_code": resetCode,
			"updated_at":          time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrUpdateResetPassword,
		}
	}

	return nil
}

func (r *UserRepository) GetStatusAndResetPasswordCode(ctx context.Context, email string) (db.UserStatus, *db.UserResetPasswordCode, error) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"status":              1,
		"reset_password_code": 1,
	}

	// Execute the query
	var result struct {
		UserStatus            db.UserStatus             `bson:"status"`
		UserResetPasswordCode *db.UserResetPasswordCode `bson:"reset_password_code"`
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

func (r *UserRepository) UpdatePasswordAndClearResetCode(ctx context.Context, email, encryptedPassword string) *models.ApiError {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define the update to set the new password and clear the reset password code
	update := bson.M{
		"$set": bson.M{
			"password":            encryptedPassword,
			"reset_password_code": nil,
			"updated_at":          time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
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
		zap.L().Error("failed to create user",
			zap.String("component", "repository"),
			zap.Error(err),
		)

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

func (r *UserRepository) GetAllUsersPag(ctx context.Context, s *db.SearchUser) ([]*db.UserResponse, int, *models.ApiError) {
	// Convert page and size to integers with default values
	page := s.Page
	if page < 1 {
		page = 1
	}

	size := s.Size
	if size < 1 {
		size = 10 // Default value for size
	}

	// Generate dynamic filters
	filters := masks.SearchUserMasks(s)

	// Define pagination options
	opts := options.Find().
		SetLimit(int64(size)).
		SetSkip(int64((page - 1) * size)).
		SetProjection(bson.M{
			"externalid":          1,
			"firstname":           1,
			"lastname":            1,
			"email":               1,
			"role":                1,
			"gender":              1,
			"status":              1,
			"profile_picture":     1,
			"description":         1,
			"phone_number":        1,
			"verification_status": 1,
			"address":             1,
			"created_at":          1,
			"updated_at":          1,
			"last_login":          1,
		})

	// Execute the query with filters and pagination options
	cursor, err := r.collection.Find(ctx, filters, opts)
	if err != nil {
		utils.Logger.Error("Failed to retrieve users from the database", zap.Error(err))
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx)

	// Count the total number of documents matching the filters
	count, err := r.collection.CountDocuments(ctx, filters)
	if err != nil {
		utils.Logger.Error("Failed to count users in the database", zap.Error(err))
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Iterate through the results and decode them
	results := make([]*db.UserResponse, 0)
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			utils.Logger.Error("Failed to decode user from the database", zap.Error(err))
			return nil, 0, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrFailedDecodeUser,
			}
		}

		// Prepare the response with user details
		userResponse := mapping.ToUserResponse(&user)
		results = append(results, &userResponse)
	}

	// Handle potential cursor errors
	if err := cursor.Err(); err != nil {
		utils.Logger.Error("Cursor error while retrieving users", zap.Error(err))
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	return results, int(count), nil
}

func (r *UserRepository) FindByFacebookID(ctx context.Context, facebookID string) (*db.User, error) {
	var user db.User
	filter := bson.M{"facebook_id": facebookID}
	err := r.collection.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByEmail retrieves a user by email
func (r *UserRepository) GetByEmailToInvite(ctx context.Context, email string) (*db.User, *models.ApiError) {
	var user db.User

	err := r.collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		utils.Logger.Error("Error retrieving user from the database", zap.String("email", email), zap.Error(err))

		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
		}
	}
	if user.UserStatus == db.Blocked {
		utils.Logger.Info("user banned",
			zap.String("email", user.UserEmail),
			zap.String("status", string(user.UserStatus)),
		)
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}
	return &user, nil
}

// GetByEmail retrieves a user by email
func (r *UserRepository) CheckEmail(ctx context.Context, email string) (*db.User, *models.ApiError) {
	var user db.User

	err := r.collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		utils.Logger.Error("Error retrieving user from the database", zap.String("email", email), zap.Error(err))

		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
		}
	}
	if user.UserStatus == db.Blocked {
		utils.Logger.Info("user banned",
			zap.String("email", user.UserEmail),
			zap.String("status", string(user.UserStatus)),
		)
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}
	return &user, nil
}

// GetByEmail retrieves a user by email
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*db.User, *models.ApiError) {
	var user db.User

	err := r.collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
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
	if user.UserStatus == db.Blocked {
		utils.Logger.Info("unable to send reset password email, user banned ",
			zap.String("email", user.UserEmail),
			zap.String("status", string(user.UserStatus)),
		)
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}
	return &user, nil
}
func (r *UserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	count, err := r.collection.CountDocuments(
		ctx,
		bson.M{"email": email},
		options.Count().SetLimit(1),
	)

	if err != nil {
		// Log the error
		utils.Logger.Error("error checking email existence",
			zap.String("email", email),
			zap.Error(err),
		)
		return false, err
	}

	// Return true if count > 0, false otherwise
	return count > 0, nil
}
func (r *UserRepository) GetByEmailCheck(ctx context.Context, email string) (*db.User, *models.ApiError) {
	var user db.User

	// Define the projection to retrieve only the required fields
	projection := bson.M{
		"token":               1,
		"externalid":          1,
		"refresh_token":       1,
		"firstname":           1,
		"lastname":            1,
		"role":                1,
		"status":              1,
		"email":               1,
		"providers":           1,
		"profile_picture":     1,
		"metadata":            1,
		"confirm_code":        1,
		"reset_password_code": 1,
		"verification_status": 1,
	}

	err := r.collection.FindOne(ctx, bson.M{"email": email}, options.FindOne().SetProjection(projection)).Decode(&user)
	if err != nil {
		if err != mongo.ErrNoDocuments {
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrRetrievingUser,
			}
		}
		return nil, nil
	}

	// Check if user is blocked
	if user.UserStatus == db.Blocked {
		utils.Logger.Info("unable to send reset password email, user banned",
			zap.String("email", user.UserEmail),
			zap.String("status", string(user.UserStatus)),
		)
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}

	return &user, nil
}

func (r *UserRepository) GetByEmailToResetPassword(ctx context.Context, email string) (*api.ResetPasswordResponse, *models.ApiError) {

	filter := bson.M{"email": email}
	projection := bson.M{
		"password":            1,
		"status":              1,
		"email":               1,
		"firstname":           1,
		"lastname":            1,
		"updated_at":          1,
		"reset_password_code": 1,
	}
	// Execute the query
	var result struct {
		UserPassword          string                    `bson:"password" validate:"required"`
		UserStatus            db.UserStatus             `bson:"status"`
		UserEmail             string                    `bson:"email" validate:"required"`
		UserFirstname         string                    `bson:"firstname" validate:"required"`
		UserLastname          string                    `bson:"lastname" validate:"required"`
		UserUpdatedAt         time.Time                 `bson:"updated_at"`
		UserResetPasswordCode *db.UserResetPasswordCode `bson:"reset_password_code"`
	}
	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
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
	if result.UserStatus == db.Blocked {
		utils.Logger.Info("unable to send reset password email, user banned ",
			zap.String("email", result.UserEmail),
			zap.String("status", string(result.UserStatus)),
		)
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}
	var resetCode db.UserResetPasswordCode
	if result.UserResetPasswordCode != nil {
		resetCode = *result.UserResetPasswordCode
	}
	resultToApi := &api.ResetPasswordResponse{
		UserPassword:          result.UserPassword,
		UserStatus:            result.UserStatus,
		UserEmail:             result.UserEmail,
		UserFirstname:         result.UserFirstname,
		UserLastname:          result.UserLastname,
		UserUpdatedAt:         result.UserUpdatedAt,
		UserResetPasswordCode: resetCode,
	}
	return resultToApi, nil
}

func (r *UserRepository) GetByEmailToConfirm(ctx context.Context, email string) (*api.GetUserConfirm, *models.ApiError) {

	filter := bson.M{"email": email}
	projection := bson.M{
		"externalid":          1,
		"verification_status": 1,
		"role":                1,
		"status":              1,
		"confirm_code":        1,
		"email":               1,
		"updated_at":          1,
		"refresh_token":       1,
	}
	// Execute the queryExternalID             string                          `bson:"externalid,omitempty"`
	var result struct {
		ExternalID             string              `bson:"externalid"`
		UserVerificationStatus bool                `bson:"verification_status"`
		UserRole               string              `bson:"role"`
		UserStatus             db.UserStatus       `bson:"status"`
		UserConfirmCode        *db.UserConfirmCode `bson:"confirm_code"`
		UserEmail              string              `bson:"email" validate:"required"`
		UserUpdatedAt          time.Time           `bson:"updated_at"`
		UserRefreshToken       string              `bson:"refresh_token"`
	}
	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
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
	if result.UserVerificationStatus {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserAlreadyVerified,
		}
	}
	resultToApi := &api.GetUserConfirm{
		UserExternalID:         result.ExternalID,
		UserVerificationStatus: result.UserVerificationStatus,
		UserRole:               result.UserRole,
		UserStatus:             result.UserStatus,
		UserConfirmCode:        result.UserConfirmCode,
		UserEmail:              result.UserEmail,
		UserUpdatedAt:          result.UserUpdatedAt,
		UserRefreshToken:       result.UserRefreshToken,
	}

	return resultToApi, nil
}

func (r *UserRepository) UpdateUserProviders(ctx context.Context, user *db.User) *models.ApiError {
	now := time.Now()

	// Extract the provider type and its details.
	// (Assumes you only have one provider in the map.)
	var providerType string
	var providerData db.OAuthProviderDetails
	for key, data := range user.Providers {
		providerType = key
		providerData = data
		break
	}
	if providerType == "" {
		utils.Logger.Error("No provider type found in user's providers")
		return &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrUpdateUser,
		}
	}

	// Construct updated provider details.
	// You can update these values based on your new data.
	providerDetails := db.OAuthProviderDetails{
		ProviderID:     providerData.ProviderID,
		Email:          user.UserEmail, // or providerData.Email if different
		FirstName:      user.UserFirstname,
		LastName:       user.UserLastname,
		ProfilePicture: user.UserProfilePicture,
		AccessToken:    providerData.AccessToken,  // updated access token if available
		RefreshToken:   providerData.RefreshToken, // updated refresh token if available
		Expiry:         providerData.Expiry,       // updated expiry if available
	}

	// Build update query with additional fields you want to update.
	update := bson.M{
		"$set": bson.M{
			fmt.Sprintf("providers.%s", providerType): providerDetails,
			"updated_at":    now,
			"last_login":    user.UserLastLogin,    // new last login value
			"token":         user.Token,            // new token value
			"refresh_token": user.UserRefreshToken, // new refresh token value
		},
	}
	// Update based on the user's email (or another unique field)
	result, err := r.collection.UpdateOne(
		ctx,
		bson.M{"email": user.UserEmail},
		update,
	)
	if err != nil {
		utils.Logger.Error("Failed to update user providers",
			zap.String("email", user.UserEmail),
			zap.Error(err),
		)
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrUpdateUser,
		}
	}

	if result.MatchedCount == 0 {
		utils.Logger.Warn("User not found for provider update",
			zap.String("email", user.UserEmail),
		)
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
	}

	utils.Logger.Info("Successfully updated user providers",
		zap.String("email", user.UserEmail),
		zap.String("provider", providerType),
	)

	return nil
}

func (r *UserRepository) Update(ctx context.Context, email string, user *db.User) *models.ApiError {

	filter := bson.M{"email": email}

	update := bson.M{
		"$set": bson.M{
			"email":               user.UserEmail,
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

func (r *UserRepository) GetUsersByExternalIds(ctx context.Context, externalIds []string) ([]db.UserResponse, *models.ApiError) {
	// Build filter using the "$in" operator.
	filter := bson.M{"externalid": bson.M{"$in": externalIds}}

	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		utils.Logger.Error("Failed to find users by external IDs", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx)

	// Collect users from cursor.
	var users []db.User
	for cursor.Next(ctx) {
		var user db.User
		if err := cursor.Decode(&user); err != nil {
			utils.Logger.Error("Failed to decode user", zap.Error(err))
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInternalError,
			}
		}
		users = append(users, user)
	}
	if err := cursor.Err(); err != nil {
		utils.Logger.Error("Cursor error", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Convert each db.User to a db.UserResponse.
	var responses []db.UserResponse
	for _, user := range users {
		response := mapping.ToUserResponse(&user)
		responses = append(responses, response)
	}

	return responses, nil
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

func (r *UserRepository) GetRefreshToken(ctx context.Context, email string) (*api.RefreshToken, *models.ApiError) {
	// Define a filter to find the user by email
	filter := bson.M{"email": email}

	// Define a projection to fetch only the required fields
	projection := bson.M{
		"externalid":    1,
		"email":         1,
		"refresh_token": 1,
		"role":          1,
		"status":        1,
	}
	// Execute the query
	var result struct {
		ExternalID       string  `bson:"externalid"`
		UserRefreshToken *string `bson:"refresh_token,omitempty"`
		UserRole         string  `bson:"role" `
		UserStatus       string  `bson:"status"`
	}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: models.ErrUserNotFound, // User not found
			}
		}
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToFetchToken, // User not found
		}
	}
	resultToApi := &api.RefreshToken{
		ExternalID:   result.ExternalID,
		UserEmail:    email,
		UserRole:     result.UserRole,
		RefreshToken: result.UserRefreshToken,
		UserStatus:   result.UserStatus,
	}
	return resultToApi, nil
}

func (r *UserRepository) UpdateToken(ctx context.Context, userExternalID string, token string) *models.ApiError {
	// Define a filter to find the user by ID
	filter := bson.M{"externalid": userExternalID}

	// Define the update to set the new token and update the timestamp
	update := bson.M{
		"$set": bson.M{
			"token":      token,
			"updated_at": time.Now(),
		},
	}

	// Execute the update operation
	_, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToUpdateUser, // User not found
		}
	}

	return nil
}

func (r *UserRepository) GetExternalIDByEmail(ctx context.Context, email string) (string, *models.ApiError) {
	var result struct {
		ExternalID string `bson:"externalid"`
	}

	filter := bson.M{"email": email}
	projection := bson.M{"externalid": 1}

	err := r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(projection)).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", &models.ApiError{
				Code:  http.StatusNotFound,
				Error: models.ErrUserNotFound,
			}
		}
		utils.Logger.Error("Error retrieving user ExternalID from the database", zap.String("email", email), zap.Error(err))
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
		}
	}

	return result.ExternalID, nil
}
func (r *UserRepository) UpdateUserByMask(ctx context.Context, externalID string, updateFields bson.M) (*db.User, *models.ApiError) {
	// MongoDB filter to find the user by ExternalID
	filter := bson.M{"externalid": externalID}

	// Add the updated timestamp to the update fields
	updateFields["updated_at"] = time.Now()

	// Define the update operation
	update := bson.M{"$set": updateFields}

	// Perform the update operation
	var updatedUser db.User
	err := r.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedUser)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, &models.ApiError{
				Code:  http.StatusNotFound,
				Error: models.ErrUserNotFound,
			}
		}
		utils.Logger.Error("Error updating user in the database", zap.String("externalID", externalID), zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrUpdateUser,
		}
	}

	return &updatedUser, nil
}

// UpdateUser updates an existing user in the MongoDB collection.
func (r *UserRepository) UpdateUser(ctx context.Context, ExternalID string, user *db.User) (*db.UserResponse, *models.ApiError) {
	// MongoDB filter to find the user by user_id
	filter := bson.M{"externalid": ExternalID}

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

// FindUserByProviderID finds a user by provider ID and type
func (r *UserRepository) FindUserByProviderID(ctx context.Context, providerType, providerID string) (*db.User, error) {
	var user db.User
	err := r.collection.FindOne(ctx, bson.M{
		"provider_type": providerType,
		"provider_id":   providerID,
	}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// FindUserByProvider finds a user by provider type and ID
func (r *UserRepository) FindUserByProvider(ctx context.Context, providerType, providerID string) (*db.User, error) {
	var user db.User
	err := r.collection.FindOne(ctx, bson.M{
		"providers": bson.M{
			"$elemMatch": bson.M{
				"type": providerType,
				"id":   providerID,
			},
		},
	}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// LinkProviderToUser links a new provider to an existing user
func (r *UserRepository) LinkProviderToUser(ctx context.Context, email string, providerType, providerID string) error {
	filter := bson.M{"email": email}
	update := bson.M{
		"$addToSet": bson.M{
			"providers": bson.M{
				"type": providerType,
				"id":   providerID,
			},
		},
	}
	_, err := r.collection.UpdateOne(ctx, filter, update)
	return err
}

// GetCoachesByPage returns a paginated list of users with role=coach, enriched with team data
// (is_head_coach, head coach id/name, team size) in a single aggregation.
func (r *UserRepository) GetCoachesByPage(ctx context.Context, page, size int, f db.AdminCoachFilter) ([]db.AdminCoachListRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	match := bson.D{{Key: "role", Value: "coach"}}
	if f.HeadCoach != nil {
		if *f.HeadCoach {
			match = append(match, bson.E{Key: "is_head_coach", Value: true})
		} else {
			match = append(match, bson.E{Key: "is_head_coach", Value: bson.D{{Key: "$ne", Value: true}}})
		}
	}
	if f.Unassigned && f.HeadCoach == nil {
		match = append(match, bson.E{Key: "is_head_coach", Value: bson.D{{Key: "$ne", Value: true}}})
	}
	if clause, ok := userSearchClause(f.Q); ok {
		match = append(match, clause)
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "coach_team_members"},
			{Key: "localField", Value: "externalid"},
			{Key: "foreignField", Value: "coach_id"},
			{Key: "as", Value: "m"},
		}}},
	}
	if f.Unassigned {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "m", Value: bson.D{{Key: "$size", Value: 0}}}}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.D{
		{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
		{Key: "data", Value: bson.A{
			bson.D{{Key: "$sort", Value: bson.D{{Key: "updated_at", Value: -1}, {Key: "externalid", Value: 1}}}},
			bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
			bson.D{{Key: "$limit", Value: int64(size)}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.collection.Name()},
				{Key: "let", Value: bson.D{{Key: "hid", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$m.head_coach_id"}}, ""}}}}}},
				{Key: "pipeline", Value: bson.A{
					bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$hid"}}}}}}},
					bson.D{{Key: "$project", Value: bson.D{{Key: "firstname", Value: 1}, {Key: "lastname", Value: 1}}}},
				}},
				{Key: "as", Value: "hc"},
			}}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: "coach_team_members"},
				{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
				{Key: "pipeline", Value: bson.A{
					bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$head_coach_id", "$$id"}}}}}}},
					bson.D{{Key: "$count", Value: "n"}},
				}},
				{Key: "as", Value: "ts"},
			}}},
			bson.D{{Key: "$project", Value: bson.D{
				{Key: "_id", Value: 0},
				{Key: "externalid", Value: 1},
				{Key: "firstname", Value: 1},
				{Key: "lastname", Value: 1},
				{Key: "email", Value: 1},
				{Key: "status", Value: 1},
				{Key: "profile_picture", Value: 1},
				{Key: "updated_at", Value: 1},
				{Key: "is_head_coach", Value: bson.D{{Key: "$eq", Value: bson.A{"$is_head_coach", true}}}},
				{Key: "head_coach_id", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$m.head_coach_id"}}, ""}}}},
				{Key: "head_coach_name", Value: bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$concat", Value: bson.A{
					bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$hc.firstname"}}, ""}}},
					" ",
					bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$hc.lastname"}}, ""}}},
				}}}}}}}},
				{Key: "team_size", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$ts.n"}}, 0}}}},
			}}},
		}},
	}}})

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		utils.Logger.Error("GetCoachesByPage: aggregate failed", zap.Error(err))
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var out []struct {
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
		Data []db.AdminCoachListRow `bson:"data"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.AdminCoachListRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	if out[0].Data == nil {
		out[0].Data = []db.AdminCoachListRow{}
	}
	return out[0].Data, total, nil
}

// SetHeadCoach flips the head-coach capability of a coach. Returns mongo.ErrNoDocuments when no such user.
func (r *UserRepository) SetHeadCoach(ctx context.Context, externalID string, flag bool, promotedBy string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	now := time.Now()
	var update bson.M
	if flag {
		update = bson.M{"$set": bson.M{
			"is_head_coach":          true,
			"head_coach_since":       now,
			"head_coach_promoted_by": promotedBy,
			"updated_at":             now,
		}}
	} else {
		update = bson.M{
			"$set":   bson.M{"is_head_coach": false, "updated_at": now},
			"$unset": bson.M{"head_coach_since": "", "head_coach_promoted_by": ""},
		}
	}
	res, err := r.collection.UpdateOne(ctx, bson.M{"externalid": externalID}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// ListHeadCoaches returns coaches flagged as head coach with their team size, in one aggregation.
func (r *UserRepository) ListHeadCoaches(ctx context.Context, page, size int, q string) ([]db.HeadCoachRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	match := bson.D{{Key: "role", Value: "coach"}, {Key: "is_head_coach", Value: true}}
	if q != "" {
		rx := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(q)}, {Key: "$options", Value: "i"}}
		match = append(match, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "firstname", Value: rx}},
			bson.D{{Key: "lastname", Value: rx}},
			bson.D{{Key: "email", Value: rx}},
		}})
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$facet", Value: bson.D{
			{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
			{Key: "data", Value: bson.A{
				bson.D{{Key: "$sort", Value: bson.D{{Key: "head_coach_since", Value: -1}, {Key: "externalid", Value: 1}}}},
				bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
				bson.D{{Key: "$limit", Value: int64(size)}},
				bson.D{{Key: "$lookup", Value: bson.D{
					{Key: "from", Value: "coach_team_members"},
					{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
					{Key: "pipeline", Value: bson.A{
						bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$head_coach_id", "$$id"}}}}}}},
						bson.D{{Key: "$count", Value: "n"}},
					}},
					{Key: "as", Value: "ts"},
				}}},
				// Current gym (a head coach has at most one membership), then the gym itself.
				bson.D{{Key: "$lookup", Value: bson.D{
					{Key: "from", Value: gymHeadCoachesCollection},
					{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
					{Key: "pipeline", Value: bson.A{
						bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$head_coach_id", "$$id"}}}}}}},
						bson.D{{Key: "$limit", Value: 1}},
						bson.D{{Key: "$lookup", Value: bson.D{
							{Key: "from", Value: gymsCollection},
							{Key: "let", Value: bson.D{{Key: "gid", Value: "$gym_id"}}},
							{Key: "pipeline", Value: bson.A{
								bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{
									"$_id",
									bson.D{{Key: "$convert", Value: bson.D{
										{Key: "input", Value: "$$gid"}, {Key: "to", Value: "objectId"}, {Key: "onError", Value: nil},
									}}},
								}}}}}}},
								bson.D{{Key: "$project", Value: bson.D{{Key: "name", Value: 1}}}},
							}},
							{Key: "as", Value: "g"},
						}}},
					}},
					{Key: "as", Value: "gh"},
				}}},
				bson.D{{Key: "$project", Value: bson.D{
					{Key: "_id", Value: 0},
					{Key: "externalid", Value: 1},
					{Key: "firstname", Value: 1},
					{Key: "lastname", Value: 1},
					{Key: "email", Value: 1},
					{Key: "profile_picture", Value: 1},
					{Key: "status", Value: 1},
					{Key: "head_coach_since", Value: 1},
					{Key: "team_size", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$ts.n"}}, 0}}}},
					{Key: "gym_id", Value: bson.D{{Key: "$first", Value: "$gh.gym_id"}}},
					{Key: "gym_name", Value: bson.D{{Key: "$first", Value: bson.D{{Key: "$first", Value: "$gh.g.name"}}}}},
				}}},
			}},
		}}},
	}

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var out []struct {
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
		Data []db.HeadCoachRow `bson:"data"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.HeadCoachRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	if out[0].Data == nil {
		out[0].Data = []db.HeadCoachRow{}
	}
	return out[0].Data, total, nil
}

// GetClientsByExternalIDs returns user records for the given external IDs (client role).
func (r *UserRepository) GetClientsByExternalIDs(ctx context.Context, ids []string) ([]*db.UserResponse, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	filter := bson.M{"externalid": bson.M{"$in": ids}}
	opts := options.Find().SetProjection(bson.M{
		"externalid":      1,
		"firstname":       1,
		"lastname":        1,
		"email":           1,
		"role":            1,
		"status":          1,
		"profile_picture": 1,
		"updated_at":      1,
	})

	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []*db.UserResponse
	for cursor.Next(ctx) {
		var u db.User
		if err := cursor.Decode(&u); err != nil {
			return nil, err
		}
		resp := mapping.ToUserResponse(&u)
		results = append(results, &resp)
	}
	return results, cursor.Err()
}

// UpdateMatriculeFiscale sets the matricule fiscale value and transitions status to pending.
func (r *UserRepository) UpdateMatriculeFiscale(ctx context.Context, externalID string, matricule string) *models.ApiError {
	now := time.Now()
	filter := bson.M{"externalid": externalID}
	update := bson.M{
		"$set": bson.M{
			"matricule_fiscale":              matricule,
			"matricule_fiscale_status":       db.MatriculeFiscalePending,
			"matricule_fiscale_submitted_at": now,
			"updated_at":                     now,
		},
	}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return &models.ApiError{
				Code:  http.StatusConflict,
				Error: fmt.Errorf("this matricule fiscale is already registered by another user"),
			}
		}
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	if result.MatchedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
	}
	return nil
}

// UpdateMatriculeFiscaleStatus transitions the matricule fiscale to approved or rejected.
func (r *UserRepository) UpdateMatriculeFiscaleStatus(ctx context.Context, externalID string, status db.MatriculeFiscaleStatus, reviewerID string) *models.ApiError {
	now := time.Now()
	filter := bson.M{"externalid": externalID}
	update := bson.M{
		"$set": bson.M{
			"matricule_fiscale_status":      status,
			"matricule_fiscale_reviewed_at": now,
			"matricule_fiscale_reviewed_by": reviewerID,
			"updated_at":                    now,
		},
	}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	if result.MatchedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusNotFound,
			Error: models.ErrUserNotFound,
		}
	}
	return nil
}

// FindByMatriculeFiscaleStatus returns paginated users filtered by matricule_fiscale_status.
func (r *UserRepository) FindByMatriculeFiscaleStatus(ctx context.Context, status db.MatriculeFiscaleStatus, page, size int) ([]*db.User, int, *models.ApiError) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	filter := bson.M{
		"role": bson.M{"$in": []string{"coach", "nutritionist"}},
	}
	if status != "" {
		filter["matricule_fiscale_status"] = status
	}

	opts := options.Find().
		SetLimit(int64(size)).
		SetSkip(int64((page - 1) * size)).
		SetSort(bson.D{{Key: "matricule_fiscale_submitted_at", Value: -1}})

	count, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx)

	var users []*db.User
	for cursor.Next(ctx) {
		var u db.User
		if err := cursor.Decode(&u); err != nil {
			return nil, 0, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrFailedDecodeUser,
			}
		}
		users = append(users, &u)
	}

	return users, int(count), nil
}

// ClearTaxRegistration removes the tax registration number and resets status to none.
// Only allowed when status is pending or rejected — approved cannot be cleared by the user.
func (r *UserRepository) ClearTaxRegistration(ctx context.Context, externalID string) *models.ApiError {
	now := time.Now()
	filter := bson.M{
		"externalid": externalID,
		"matricule_fiscale_status": bson.M{"$in": []db.MatriculeFiscaleStatus{
			db.MatriculeFiscalePending,
			db.MatriculeFiscaleRejected,
		}},
	}
	update := bson.M{
		"$unset": bson.M{
			"matricule_fiscale":              "",
			"matricule_fiscale_submitted_at": "",
			"matricule_fiscale_reviewed_at":  "",
			"matricule_fiscale_reviewed_by":  "",
		},
		"$set": bson.M{
			"matricule_fiscale_status": db.MatriculeFiscaleNone,
			"updated_at":               now,
		},
	}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	if result.MatchedCount == 0 {
		return &models.ApiError{
			Code:  http.StatusConflict,
			Error: fmt.Errorf("cannot withdraw an approved tax registration number"),
		}
	}
	return nil
}

// GetStatusByExternalID returns only the account status; used by the auth middleware re-check.
func (r *UserRepository) GetStatusByExternalID(ctx context.Context, externalID string) (db.UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var result struct {
		UserStatus db.UserStatus `bson:"status"`
	}
	err := r.collection.FindOne(ctx, bson.M{"externalid": externalID},
		options.FindOne().SetProjection(bson.M{"status": 1})).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", models.ErrUserNotFound
		}
		return "", models.ErrFailedToFetchUserStatus
	}
	return result.UserStatus, nil
}

// consumeAttempt atomically counts one verification try on the code stored at field and returns
// the code as it is after the increment. It returns (nil, nil) when the user has no such code.
func (r *UserRepository) consumeAttempt(ctx context.Context, email, field string, out interface{}) (bool, *models.ApiError) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	filter := bson.M{"email": email, field: bson.M{"$ne": nil}}
	update := bson.M{"$inc": bson.M{field + ".attempts": 1}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{field: 1})
	err := r.collection.FindOneAndUpdate(ctx, filter, update, opts).Decode(out)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	return true, nil
}

// ConsumeResetAttempt counts one reset-code try and returns the stored code after the increment.
func (r *UserRepository) ConsumeResetAttempt(ctx context.Context, email string) (*db.UserResetPasswordCode, *models.ApiError) {
	var doc struct {
		Code *db.UserResetPasswordCode `bson:"reset_password_code"`
	}
	found, apiErr := r.consumeAttempt(ctx, email, "reset_password_code", &doc)
	if apiErr != nil || !found {
		return nil, apiErr
	}
	return doc.Code, nil
}

// ConsumeConfirmAttempt counts one confirmation-code try and returns the stored code after the increment.
func (r *UserRepository) ConsumeConfirmAttempt(ctx context.Context, email string) (*db.UserConfirmCode, *models.ApiError) {
	var doc struct {
		Code *db.UserConfirmCode `bson:"confirm_code"`
	}
	found, apiErr := r.consumeAttempt(ctx, email, "confirm_code", &doc)
	if apiErr != nil || !found {
		return nil, apiErr
	}
	return doc.Code, nil
}

// lockCode invalidates the code at field and blocks issuing a new one until lockedUntil.
// DoS tradeoff: anyone who knows an email can burn its 5-guess budget and lock the owner out of
// that code flow for core.CodeLockout (15 min). Accepted because the alternative (no lock) lets
// an attacker brute-force the 6-digit code. The lock is temporary, a right code still works while
// not locked, and the budget is never refilled by resending (see core.PlanIssue).
func (r *UserRepository) lockCode(ctx context.Context, email, field string, lockedUntil time.Time) *models.ApiError {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{
		field + ".code":        "",
		field + ".lockeduntil": lockedUntil,
	}}
	if _, err := r.collection.UpdateOne(ctx, bson.M{"email": email, field: bson.M{"$ne": nil}}, update); err != nil {
		return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	return nil
}

// LockResetCode invalidates the reset code after too many failed tries.
func (r *UserRepository) LockResetCode(ctx context.Context, email string, lockedUntil time.Time) *models.ApiError {
	return r.lockCode(ctx, email, "reset_password_code", lockedUntil)
}

// LockConfirmCode invalidates the confirmation code after too many failed tries.
func (r *UserRepository) LockConfirmCode(ctx context.Context, email string, lockedUntil time.Time) *models.ApiError {
	return r.lockCode(ctx, email, "confirm_code", lockedUntil)
}
