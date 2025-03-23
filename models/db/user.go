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
	ID                     primitive.ObjectID              `bson:"_id,omitempty"`
	ExternalID             string                          `bson:"externalid,omitempty"`
	UserFirstname          string                          `bson:"firstname" validate:"required"`
	UserLastname           string                          `bson:"lastname" validate:"required"`
	UserEmail              string                          `bson:"email" validate:"required"`
	UserPassword           string                          `bson:"password" validate:"required"`
	UserRole               string                          `bson:"role" `
	UserProfilePicture     string                          `bson:"profile_picture,omitempty"`
	UserDescription        string                          `bson:"description,omitempty"`
	UserPhoneNumber        string                          `bson:"phone_number,omitempty"`
	Token                  *string                         `bson:"token"`
	Providers              map[string]OAuthProviderDetails `bson:"providers,omitempty"`
	UserRefreshToken       *string                         `bson:"refresh_token,omitempty"`
	UserVerificationStatus bool                            `bson:"verification_status"`
	Autologin              bool                            `bson:"autologin"`
	UserGender             UserGender                      `bson:"gender"`
	UserStatus             UserStatus                      `bson:"status"`
	UserConfirmCode        *UserConfirmCode                `bson:"confirm_code"`
	UserResetPasswordCode  *UserResetPasswordCode          `bson:"reset_password_code"`
	UserAddress            Address                         `bson:"address,omitempty"`
	Metadata               *UserMetadata                   `bson:"metadata,omitempty" json:"metadata,omitempty"`
	UserCreatedAt          time.Time                       `bson:"created_at"`
	UserUpdatedAt          time.Time                       `bson:"updated_at"`
	UserLastLogin          time.Time                       `bson:"last_login"`
}
type UserMetadata struct {
	HowHeardAboutUs string   `bson:"how_heard_about_us,omitempty" json:"how_heard_about_us,omitempty"`
	Profession      string   `bson:"profession,omitempty" json:"profession,omitempty"`
	WorkPlace       string   `bson:"work_place,omitempty" json:"work_place,omitempty"`
	ClientRange     string   `bson:"client_range,omitempty" json:"client_range,omitempty"`
	Offerings       []string `bson:"offerings,omitempty" json:"offerings,omitempty"`
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
	Page                   int        `bson:"page"`
	Size                   int        `bson:"size"`
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
	UserFirstname      string        `bson:"firstname" `
	UserLastname       string        `bson:"lastname" `
	UserEmail          string        `bson:"email"`
	UserRole           string        `bson:"role"`
	UserGender         string        `bson:"gender"`
	UserStatus         string        `bson:"status"`
	UserProfilePicture string        `bson:"profile_picture,omitempty"`
	UserDescription    string        `bson:"description,omitempty"`
	UserPhoneNumber    string        `bson:"phone_number,omitempty"`
	Autologin          bool          `bson:"autologin"`
	VerificationStatus bool          `bson:"verification_status"`
	UserAddress        Address       `bson:"address,omitempty"`
	Metadata           *UserMetadata `bson:"metadata,omitempty" json:"metadata,omitempty"`
	UserUpdatedAt      time.Time     `bson:"updated_at"`
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
	Metadata           *UserMetadata      `bson:"metadata,omitempty" json:"metadata,omitempty"`
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

type OAuthUser struct {
	ProviderType   string    `bson:"provider_type" json:"provider_type"`
	ProviderID     string    `bson:"provider_id" json:"provider_id"`
	Email          string    `bson:"email" json:"email"`
	FirstName      string    `bson:"first_name" json:"first_name"`
	LastName       string    `bson:"last_name" json:"last_name"`
	ProfilePicture string    `bson:"profile_picture" json:"profile_picture"`
	AccessToken    string    `bson:"access_token" json:"access_token"`   // Encrypted
	RefreshToken   string    `bson:"refresh_token" json:"refresh_token"` // Encrypted
	Expiry         time.Time `bson:"expiry" json:"expiry"`
}

type OAuthUserApi struct {
	ProviderID     string    `json:"provider_id"`
	Email          string    `json:"email"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	ProfilePicture string    `json:"profile_picture"`
	AccessToken    string    `json:"access_token"`  // Encrypted
	RefreshToken   string    `json:"refresh_token"` // Encrypted
	Expiry         time.Time `json:"expiry"`
}
type OAuthProviderDetails struct {
	ProviderID     string    `bson:"provider_id"`
	Email          string    `bson:"email"`
	FirstName      string    `bson:"first_name"`
	LastName       string    `bson:"last_name"`
	ProfilePicture string    `bson:"profile_picture"`
	AccessToken    string    `bson:"access_token"`  // Encrypted
	RefreshToken   string    `bson:"refresh_token"` // Encrypted
	Expiry         time.Time `bson:"expiry"`
}
