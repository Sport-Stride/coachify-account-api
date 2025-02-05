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
func ToRefreshToken(dbUser *db.User) api.RefreshToken {
	return api.RefreshToken{
		ExternalID:   dbUser.ExternalID,
		UserEmail:    dbUser.UserEmail,
		UserRole:     dbUser.UserRole,
		RefreshToken: dbUser.UserRefreshToken,
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
func UpdateUserAPIToDB(User api.UserRequest) db.User {

	return db.User{
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
