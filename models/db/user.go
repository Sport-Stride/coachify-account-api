package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type UserConfirmCode struct {
	Code           string    `json:"code"`
	ExpirationDate time.Time `json:"expiration_date"`
}

type UserResetPasswordCode struct {
	Code           string    `json:"code"`
	ExpirationDate time.Time `json:"expiration_date"`
}

type User struct {
	ID                     primitive.ObjectID     `bson:"_id,omitempty"`
	ExternalID             string                 `bson:"externalid"`
	UserFirstname          string                 `bson:"firstname" validate:"required"`
	UserLastname           string                 `bson:"lastname" validate:"required"`
	UserEmail              string                 `bson:"email" validate:"required"`
	UserPassword           string                 `bson:"password" validate:"required"`
	UserRole               string                 `bson:"role" `
	UserProfilePicture     string                 `bson:"profile_picture,omitempty"`
	UserDescription        string                 `bson:"description,omitempty"`
	UserPhoneNumber        string                 `bson:"phone_number,omitempty"`
	Token                  *string                `bson:"token"`
	UserRefreshToken       *string                `bson:"refresh_token,omitempty"`
	UserVerificationStatus bool                   `bson:"verification_status"`
	Autologin              bool                   `bson:"autologin"`
	UserGender             UserGender             `bson:"gender"`
	UserStatus             UserStatus             `bson:"status"`
	UserConfirmCode        *UserConfirmCode       `bson:"confirm_code"`
	UserResetPasswordCode  *UserResetPasswordCode `bson:"reset_password_code"`
	UserAddress            Address                `bson:"address,omitempty"`
	UserCreatedAt          time.Time              `bson:"created_at"`
	UserUpdatedAt          time.Time              `bson:"updated_at"`
	UserLastLogin          time.Time              `bson:"last_login"`
}

type SearchUser struct {
	ExternalID             string     `bson:"externalid"`
	UserFirstname          string     `bson:"firstname"  `
	UserLastname           string     `bson:"lastname" `
	UserEmail              string     `bson:"email" `
	UserPassword           string     `bson:"password" `
	UserRole               string     `bson:"role" `
	UserProfilePicture     string     `bson:"profile_picture,omitempty"`
	UserDescription        string     `bson:"description,omitempty"`
	UserPhoneNumber        string     `bson:"phone_number,omitempty"`
	Page                   string     `bson:"page"`
	Size                   string     `bson:"size"`
	UserVerificationStatus bool       `bson:"verification_status"`
	VerificationStatusSet  bool       `bson:"verification_status_set"`
	UserGender             UserGender ` bson:"gender"`
	UserStatus             UserStatus ` bson:"status"`
	UserAddress            *Address   `bson:"address,omitempty"`
	UserCreatedAt          time.Time  `bson:"created_at"`
	UserUpdatedAt          time.Time  `bson:"updated_at"`
	UserLastLogin          time.Time  `bson:"last_login"`
}

type UserRequest struct {
	UserFirstname      string    `bson:"firstname" `
	UserLastname       string    `bson:"lastname" `
	UserEmail          string    `bson:"email"`
	UserRole           string    `bson:"role"`
	UserGender         string    `bson:"gender"`
	UserStatus         string    `bson:"status"`
	UserProfilePicture string    `bson:"profile_picture,omitempty"`
	UserDescription    string    `bson:"description,omitempty"`
	UserPhoneNumber    string    `bson:"phone_number,omitempty"`
	Autologin          bool      `bson:"autologin"`
	VerificationStatus bool      `bson:"verification_status"`
	UserAddress        Address   `bson:"address,omitempty"`
	UserUpdatedAt      time.Time `bson:"updated_at"`
}

type UserResponse struct {
	Id                 primitive.ObjectID `bson:"id"`
	ExternalID         string             `bson:"external_id,omitempty"`
	Firstname          string             `bson:"firstname"`
	Lastname           string             `bson:"lastname"`
	Email              string             `bson:"email"`
	Password           string             `bson:"password"`
	Role               string             `bson:"role"`
	Gender             string             `bson:"gender"`
	Status             string             `bson:"status"`
	ProfilePicture     string             `bson:"profile_picture,omitempty"`
	Description        string             `bson:"description,omitempty"`
	PhoneNumber        string             `bson:"phone_number,omitempty"`
	Token              *string            `bson:"token"`
	RefreshToken       *string            `bson:"refresh_token,omitempty"`
	Autologin          bool               `bson:"autologin"`
	VerificationStatus bool               `bson:"verification_status"`
	Address            Address            `bson:"address,omitempty"`
	CreatedAt          time.Time          `bson:"created_at"`
	UpdatedAt          time.Time          `bson:"updated_at"`
	LastLogin          time.Time          `bson:"last_login"`
}

type Address struct {
	City       *string `bson:"city,omitempty" form:"city"`
	Country    *string `bson:"country,omitempty" form:"country"`
	Line1      *string `bson:"line1,omitempty" form:"line1"`
	Line2      *string `bson:"line2,omitempty" form:"line2"`
	PostalCode *string `bson:"postal_code,omitempty" form:"postal_code"`
	State      *string `bson:"state,omitempty" form:"state"`
}
