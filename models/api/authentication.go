package api

import (
	"coachify-account-api/models/db"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type ConfirmResetPasswordEmailQuery struct {
	Name     string
	Email    string
	Location string
	IP       string
	Date     string
}

type ConfirmResetPasswordRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

type DeleteUserRequest struct {
	ExternalID string `json:"external_id,omitempty"`
}

var ResetPassword struct {
	UserPassword  string        `bson:"password" validate:"required"`
	UserStatus    db.UserStatus `bson:"status"`
	UserEmail     string        `bson:"email" validate:"required"`
	UserUpdatedAt time.Time     `bson:"updated_at"`
}

type ResetPasswordResponse struct {
	UserPassword          string                   `bson:"password" validate:"required"`
	UserStatus            db.UserStatus            `bson:"status"`
	UserEmail             string                   `bson:"email" validate:"required"`
	UserUpdatedAt         time.Time                `bson:"updated_at"`
	UserResetPasswordCode db.UserResetPasswordCode `bson:"reset_password_code"`
}
type ConfirmUserRequest struct {
	Email       string `json:"email"`
	ConfirmCode string `json:"code"`
}

type LoginRequest struct {
	Email     string `json:"username"`
	Password  string `json:"password"`
	Autologin bool   `json:"autologin"`
}

type ResendConfirmUserRequest struct {
	Email string `json:"email" binding:"required"`
}

type ResetPasswordRequest struct {
	Email string `json:"email" binding:"required"`
}

type OAuthResponse struct {
	User ApiUser `json:"user"`
}

type CreateUserRequest struct {
	FirstName   string     `json:"firstname" validate:"required"`
	LastName    string     `json:"lastname" validate:"required"`
	Email       string     `json:"email" validate:"required"`
	Password    string     `json:"password" validate:"required"`
	PhoneNumber string     `json:"phone_number,omitempty"`
	Role        string     `json:"role"`
	Autologin   bool       `json:"autologin"`
	Address     Address    `json:"address,omitempty"`
	Gender      UserGender `json:"gender,omitempty"`
	Status      UserStatus `json:"status" `
	jwt.StandardClaims
}

type LoginResponse struct {
	User ApiUser `json:"user"`
}

type RegisterResponse struct {
	User        ApiUserResponse `json:"user"`
	AuthToken   string          `json:"auth_token,omitempty"`
	RereshToken string          `json:"reresh_token"`
}

type ApiUser struct {
	ID                 string                    `json:"id"`
	ExternalID         string                    `json:"external_id,omitempty"`
	Firstname          string                    `json:"firstname"`
	Lastname           string                    `json:"lastname"`
	Email              string                    `json:"email"`
	Password           string                    `json:"password"`
	Role               string                    `json:"role"`
	Gender             string                    `json:"gender"`
	Status             string                    `json:"status"`
	ProfilePicture     string                    `json:"profile_picture,omitempty"`
	Description        string                    `json:"description,omitempty"`
	PhoneNumber        string                    `json:"phone_number,omitempty"`
	Token              *string                   `json:"token"`
	RefreshToken       *string                   `json:"refresh_token,omitempty"`
	VerificationStatus bool                      `json:"verification_status"`
	Autologin          bool                      `json:"autologin"`
	Address            Address                   `json:"address,omitempty"`
	ConfirmCode        db.UserConfirmCode        `json:"confirm_code,omitempty"`
	ResetPasswordCode  *db.UserResetPasswordCode `json:"reset_password_code"`
	Metadata           *db.UserMetadata          `bson:"metadata,omitempty" json:"metadata,omitempty"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	LastLogin          time.Time                 `json:"last_login"`
}

type ApiUserResponse struct {
	ID                 string           `json:"id"`
	ExternalID         string           `json:"external_id,omitempty"`
	Firstname          string           `json:"firstname"`
	Lastname           string           `json:"lastname"`
	Email              string           `json:"email"`
	Password           string           `json:"password"`
	Role               string           `json:"role"`
	Gender             string           `json:"gender"`
	Status             string           `json:"status"`
	ProfilePicture     string           `json:"profile_picture,omitempty"`
	Description        string           `json:"description,omitempty"`
	PhoneNumber        string           `json:"phone_number,omitempty"`
	Token              *string          `json:"token"`
	RefreshToken       *string          `json:"refresh_token,omitempty"`
	VerificationStatus bool             `json:"verification_status"`
	Autologin          bool             `json:"autologin"`
	Address            Address          `json:"address,omitempty"`
	Metadata           *db.UserMetadata `bson:"metadata,omitempty" json:"metadata,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	LastLogin          time.Time        `json:"last_login"`
}

type RequestRefreshtoken struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
}

type UserRequest struct {
	ExternalID         string           `json:"external_id,omitempty"`
	UserFirstname      string           `json:"firstname" validate:"required"`
	UserLastname       string           `json:"lastname" validate:"required"`
	UserEmail          string           `json:"email"`
	UserRole           string           `json:"role"`
	UserGender         string           `json:"gender"`
	UserStatus         string           `json:"status"`
	UserProfilePicture string           `json:"profile_picture,omitempty"`
	UserDescription    string           `json:"description,omitempty"`
	UserPhoneNumber    string           `json:"phone_number,omitempty"`
	Autologin          bool             `json:"autologin"`
	VerificationStatus bool             `json:"verification_status"`
	Metadata           *db.UserMetadata `bson:"metadata,omitempty" json:"metadata,omitempty"`
	UserAddress        Address          `json:"address,omitempty"`
}

type RefreshToken struct {
	ExternalID    string  `json:"external_id,omitempty"`
	UserFirstname string  `json:"firstname" validate:"required"`
	UserLastname  string  `json:"lastname" validate:"required"`
	UserEmail     string  `bson:"email" validate:"required"`
	UserRole      string  `json:"role"`
	RefreshToken  *string `json:"refresh_token,omitempty"`
}

type GetUserConfirm struct {
	UserVerificationStatus bool                `bson:"verification_status"`
	UserRole               string              `bson:"role"`
	UserStatus             db.UserStatus       `bson:"status"`
	UserConfirmCode        *db.UserConfirmCode `bson:"confirm_code"`
	UserEmail              string              `bson:"email" validate:"required"`
	UserUpdatedAt          time.Time           `bson:"updated_at"`
}

type ConfirmResponse struct {
	ExternalID             string              `json:"externalId"`
	UserEmail              string              `json:"userEmail"`
	UserRole               string              `json:"userRole"`
	UserVerificationStatus bool                `json:"userVerificationStatus"`
	UserStatus             db.UserStatus       `bson:"status"`
	UserConfirmCode        *db.UserConfirmCode `json:"userConfirmCode,omitempty"`
}

type SearchUser struct {
	ExternalID            string    `json:"external_id,omitempty"`
	Firstname             string    `json:"firstname"`
	Lastname              string    `json:"lastname"`
	Email                 string    `json:"email"`
	Role                  string    `json:"role"`
	Gender                string    `json:"gender"`
	Status                string    `json:"status"`
	ProfilePicture        string    `json:"profile_picture,omitempty"`
	Description           string    `json:"description,omitempty"`
	PhoneNumber           string    `json:"phone_number,omitempty"`
	Page                  int       `json:"page"`
	Size                  int       `json:"size"`
	VerificationStatus    bool      `json:"verification_status"`
	VerificationStatusSet bool      `json:"verification_status_set"`
	Address               *Address  `json:"address,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	LastLogin             time.Time `json:"last_login"`
}

type RequestUpdateUser struct {
	User        UserRequest `json:"user,omitempty"`
	UpdateMasks []string    `json:"update_masks"`
}

type Address struct {
	City       *string `json:"city,omitempty" form:"city"`
	Country    *string `json:"country,omitempty" form:"country"`
	Line1      *string `json:"line1,omitempty" form:"line1"`
	Line2      *string `json:"line2,omitempty" form:"line2"`
	PostalCode *string `json:"postal_code,omitempty" form:"postal_code"`
	State      *string `json:"state,omitempty" form:"state"`
}
