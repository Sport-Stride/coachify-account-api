package mapping

import (
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"time"
)

func CreateToDbUser(req *api.CreateUserRequest, encryptedPassword string, id string, confirmCode db.UserConfirmCode) *db.User {
	now := time.Now()

	return &db.User{
		UserFirstname:          req.FirstName,
		UserLastname:           req.LastName,
		UserEmail:              req.Email,
		UserPassword:           encryptedPassword,
		UserRole:               req.Role,
		UserGender:             db.UserGender(req.Gender),
		UserStatus:             db.UserStatus(req.Status),
		UserProfilePicture:     "",
		UserDescription:        "",
		UserPhoneNumber:        req.PhoneNumber,
		UserAddress:            db.Address(req.Address),
		UserVerificationStatus: false,
		UserCreatedAt:          now,
		UserUpdatedAt:          now,
		Autologin:              req.Autologin,
		ExternalID:             id,
		UserConfirmCode:        &confirmCode,
	}
}

// ToDbUserFromGoogleProfile maps a GoogleProfile payload to a new User model for the database.
// ToDbUserFromGoogleProfile maps a GoogleLoginRequest payload to a new User model for the database.
func ToDbUserFromGoogleProfile(googleLoginRequest db.GoogleLoginRequest, externalid string) *db.User {
	return &db.User{
		ExternalID:         externalid,
		UserFirstname:      googleLoginRequest.Profile.GivenName,
		UserLastname:       googleLoginRequest.Profile.FamilyName,
		UserEmail:          googleLoginRequest.Profile.Email,
		UserProfilePicture: googleLoginRequest.Profile.Picture,
		UserStatus:         db.Active, // Active because it's an OAuth login
		UserCreatedAt:      time.Now(),
		UserUpdatedAt:      time.Now(),
		UserRole:           googleLoginRequest.Role,
		Providers: map[string]db.OAuthProviderDetails{
			googleLoginRequest.Profile.ProviderType: {
				ProviderID:     googleLoginRequest.Profile.Sub,
				Email:          googleLoginRequest.Profile.Email,
				FirstName:      googleLoginRequest.Profile.GivenName,
				LastName:       googleLoginRequest.Profile.FamilyName,
				ProfilePicture: googleLoginRequest.Profile.Picture,
				AccessToken:    googleLoginRequest.Account.AccessToken,
				RefreshToken:   googleLoginRequest.Account.RefreshToken,
				Expiry:         time.Unix(googleLoginRequest.Account.ExpiresAt, 0), // Convert ExpiresAt from epoch seconds to time.Time
			},
		},
		UserConfirmCode:    &db.UserConfirmCode{Code: "", ExpirationDate: time.Now()},
	}
}

// ToDbUserFromOAuth maps an OAuthUser struct to a new User model for the database
func ToDbUserFromOAuth(oauthUser db.OAuthUser, externalid string) *db.User {
	return &db.User{
		ExternalID:         externalid,
		UserFirstname:      oauthUser.FirstName,
		UserLastname:       oauthUser.LastName,
		UserEmail:          oauthUser.Email,
		UserProfilePicture: oauthUser.ProfilePicture,
		UserStatus:         db.Active, // Active because it's an OAuth login
		UserCreatedAt:      time.Now(),
		UserUpdatedAt:      time.Now(),
		Providers: map[string]db.OAuthProviderDetails{
			oauthUser.ProviderType: {
				ProviderID:     oauthUser.ProviderID,
				Email:          oauthUser.Email,
				FirstName:      oauthUser.FirstName,
				LastName:       oauthUser.LastName,
				ProfilePicture: oauthUser.ProfilePicture,
				AccessToken:    oauthUser.AccessToken,
				RefreshToken:   oauthUser.RefreshToken,
				Expiry:         oauthUser.Expiry,
			},
		},
	}
}

func ToRefreshToken(dbUser *db.User) api.RefreshToken {
	return api.RefreshToken{
		ExternalID:    dbUser.ExternalID,
		UserFirstname: dbUser.UserFirstname,
		UserLastname:  dbUser.UserLastname,
		UserEmail:     dbUser.UserEmail,
		UserRole:      dbUser.UserRole,
		RefreshToken:  dbUser.UserRefreshToken,
	}
}

func ToConfirmResponse(dbUser *api.GetUserConfirm) api.ConfirmResponse {
	return api.ConfirmResponse{
		UserEmail:              dbUser.UserEmail,
		UserVerificationStatus: dbUser.UserVerificationStatus,
		UserStatus:             dbUser.UserStatus,
		UserConfirmCode:        dbUser.UserConfirmCode,
	}
}

func ToResetPasswor(dbUser *api.ResetPasswordResponse) api.ResetPasswordResponse {
	return api.ResetPasswordResponse{
		UserPassword:  dbUser.UserPassword,
		UserStatus:    dbUser.UserStatus,
		UserEmail:     dbUser.UserEmail,
		UserUpdatedAt: dbUser.UserUpdatedAt,
	}
}

func ToApiUser(dbUser *db.User) api.ApiUser {
	var confirmCode db.UserConfirmCode
	if dbUser.UserConfirmCode != nil {
		confirmCode = *dbUser.UserConfirmCode
	}

	var resetPasswordCode db.UserResetPasswordCode
	if dbUser.UserResetPasswordCode != nil {
		resetPasswordCode = *dbUser.UserResetPasswordCode
	}

	return api.ApiUser{
		ID:                 dbUser.ID.Hex(),
		Firstname:          dbUser.UserFirstname,
		Lastname:           dbUser.UserLastname,
		Email:              dbUser.UserEmail,
		Password:           dbUser.UserPassword,
		Role:               dbUser.UserRole,
		Gender:             string(dbUser.UserGender),
		Status:             string(dbUser.UserStatus),
		ProfilePicture:     dbUser.UserProfilePicture,
		Description:        dbUser.UserDescription,
		PhoneNumber:        dbUser.UserPhoneNumber,
		Address:            api.Address(dbUser.UserAddress),
		VerificationStatus: dbUser.UserVerificationStatus,
		CreatedAt:          dbUser.UserCreatedAt,
		UpdatedAt:          dbUser.UserUpdatedAt,
		ConfirmCode:        confirmCode,
		ResetPasswordCode:  &resetPasswordCode,
		RefreshToken:       dbUser.UserRefreshToken,
		Token:              dbUser.Token,
		Metadata:           dbUser.Metadata,
		LastLogin:          dbUser.UserLastLogin,
		ExternalID:         dbUser.ExternalID,
		Autologin:          dbUser.Autologin,
	}
}

func ToApiUserResponse(dbUser *db.UserResponse) api.ApiUserResponse {

	return api.ApiUserResponse{
		ID:        dbUser.Id.Hex(),
		Firstname: dbUser.Firstname,
		Lastname:  dbUser.Lastname,
		Email:     dbUser.Email,
		Password:  dbUser.Password,
		Role:      dbUser.Role,

		Gender:             dbUser.Gender,
		Status:             dbUser.Status,
		ProfilePicture:     dbUser.ProfilePicture,
		Description:        dbUser.Description,
		PhoneNumber:        dbUser.PhoneNumber,
		Address:            api.Address(dbUser.Address),
		VerificationStatus: dbUser.VerificationStatus,
		CreatedAt:          dbUser.CreatedAt,
		UpdatedAt:          dbUser.UpdatedAt,

		RefreshToken: dbUser.RefreshToken,
		Token:        dbUser.Token,
		Metadata:     dbUser.Metadata,
		LastLogin:    dbUser.LastLogin,
		ExternalID:   dbUser.ExternalID,
		Autologin:    dbUser.Autologin,
	}
}

func ToUserResponse(user *db.User) db.UserResponse {
	return db.UserResponse{
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
		Metadata:           user.Metadata,
	}
}

func FormattedDateTime() string {
	return time.Now().Format("02/01/2006 15:04:05")
}

type PaginatedUser struct {
	Users        []*api.ApiUserResponse `json:"users"`
	Page         int                    `json:"page"`
	TotalPerPage int                    `json:"total_per_page"`
	Total        int                    `json:"total"`
	TotalPages   int                    `json:"total_pages"`
}

func SearchUserAPIToDB(searchUser api.SearchUser) db.SearchUser {

	return db.SearchUser{
		UserFirstname:          searchUser.Firstname,
		UserLastname:           searchUser.Lastname,
		UserEmail:              searchUser.Email,
		UserRole:               searchUser.Role,
		UserGender:             db.UserGender(searchUser.Gender),
		UserStatus:             db.UserStatus(searchUser.Status),
		UserProfilePicture:     searchUser.ProfilePicture,
		UserDescription:        searchUser.Description,
		UserPhoneNumber:        searchUser.PhoneNumber,
		UserAddress:            (*db.Address)(searchUser.Address),
		UserVerificationStatus: searchUser.VerificationStatus,
		VerificationStatusSet:  searchUser.VerificationStatusSet,
		UserCreatedAt:          searchUser.CreatedAt,
		UserUpdatedAt:          searchUser.UpdatedAt,
		UserLastLogin:          searchUser.LastLogin,
		ExternalID:             searchUser.ExternalID,
		Page:                   searchUser.Page,
		Size:                   searchUser.Size,
	}
}
func UpdateUserAPIToDB(User api.UserRequest, ExternalID string) db.User {

	return db.User{
		ExternalID:             ExternalID,
		UserFirstname:          User.UserFirstname,
		UserLastname:           User.UserLastname,
		UserEmail:              User.UserEmail,
		UserRole:               User.UserRole,
		UserGender:             db.UserGender(User.UserGender),
		UserStatus:             db.UserStatus(User.UserStatus),
		UserProfilePicture:     User.UserProfilePicture,
		UserDescription:        User.UserDescription,
		UserPhoneNumber:        User.UserPhoneNumber,
		UserAddress:            db.Address(User.UserAddress),
		UserVerificationStatus: User.VerificationStatus,
		Autologin:              User.Autologin,
	}
}
