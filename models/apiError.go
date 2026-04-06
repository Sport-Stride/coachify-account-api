package models

import (
	"errors"
	"fmt"
)

// ApiError represents an API error response.
type ApiError struct {
	Code  int   // HTTP status code
	Error error // Underlying error
}

var (
	ErrInvalidIdStringGeneration = errors.New("length must be greater than 0")
	ErrInvalidIdFormat           = errors.New("invalid user ID format")
	ErrInvalidInputInUpdateMask  = errors.New("invalid input: dataDB and dataReq cannot be nil")
	ErrFailedToUpdateUser        = errors.New("failed to update user")
	ErrUpdateResetPassword       = errors.New("failed to update reset password")
	ErrEmailNotProvided          = errors.New("email address is missing")
	ErrUnableToSendResPass       = errors.New("unable to send reset password email")
	ErrInvalidTokenType          = errors.New("invalid token type")
	ErrSigningToken              = errors.New("error signing token")
	ErrParsingSignedToken        = errors.New("error parsing signed token")
	ErrFailedToHashPassword      = errors.New("failed to hash password")
	ErrRetrievingUser            = errors.New("error retrieving user")
	ErrPasswordMismatch          = errors.New("password mismatch")
	ErrFailedToFetchVerifStatus  = errors.New("failed to fetch verification status")
	ErrFailedToFetchUserStatus   = errors.New("failed to fetch user status")
	ErrFailedToVerifyPassword    = errors.New("failed to verify password")
	ErrInternalError             = errors.New("internal server error")
	ErrUpdateUser                = errors.New("failed to update user")
	ErrNoChangesToUser           = errors.New("no changes made to user")
	ErrUserNotFound              = errors.New("user not found")
	ErrFailedDecodeUser          = errors.New("failed to decode user")
	ErrEmailAlreadyExists        = errors.New("email already exists")
	ErrFailedToCreateUser        = errors.New("failed to create user")
	ErrInvalidPassword           = errors.New("the password must contain at least 8 characters, one uppercase letter, one lowercase letter, and one symbol")
	ErrUserAlreadyVerified       = errors.New("user already verified")
	ErrInvalidConfirmationCode   = errors.New("invalid confirmation code")
	ErrInvitationNotAccepted     = errors.New("invitation not accepted")
	ErrUserBlocked               = errors.New("account is blocked")
	ErrAccountNotConfirmed       = errors.New("account not confirmed")
	ErrAuthenticationFailed      = errors.New("authentication failed")
	ErrIncorrectPassword         = errors.New("incorrect password provided")
	ErrUnknownUser               = errors.New("unknown user")
	ErrInvalidResetPasswordCode  = errors.New("invalid reset password code")
	ErrErrorGeneratingJWTToken   = errors.New("error generating jwt token")
	ErrLocalServerError          = errors.New("local server error")
	ErrUnableToUpdateLastLogin   = errors.New("unable to update last login")
	ErrUnableToUpdateUser        = errors.New("unable to update user")
	ErrInvalidRefreshToken       = errors.New("invalid refresh token")
	ErrDbUserIsNil               = errors.New("dbUser is nil")
	ErrFailedToSendEmail         = errors.New("failed to send email")
	ErrUserAlreadyExists         = errors.New("user already exists")
	ErrUserCreationFailed        = errors.New("user creation failed")
	ErrUserUpdateFailed          = errors.New("user update failed")
	ErrUserDeletionFailed        = errors.New("user deletion failed")
	ErrPasswordVerificationError = errors.New("error verifying password")
	ErrOAuthEmailMismatch        = errors.New("oauth email mismatch: you must sign up with the email your invitation was sent to")
	ErrFailedToCreateRequest     = errors.New("failed to create request to identifier service")
	ErrFailedToSendRequest       = errors.New("failed to send request to identifier service")
	ErrFailedToReadResponse      = errors.New("error reading response body")
	ErrFailedToUnmarshalResponse = errors.New("error unmarshaling response body")
	ErrFailedToExchangeCode      = errors.New("error exchanging provider code")
	ErrProviderNotFound          = errors.New("error provider dosen't exist")
	ErrBadRequestToIdentifier    = errors.New("bad request to identifier service")
	ErrUnexpectedStatusCode      = errors.New("unexpected status code from identifier service")
	ErrFailedToFetchToken        = errors.New("failed to fetch refresh token")
	ErrInvalidIDToken            = errors.New("invalid id token")
	ErrTokenEncryptionFailed     = errors.New("token encryption failed")
	ErrInvalidAudience           = errors.New("invalid audience")
	ErrClientAlreadyLinked       = errors.New("client is already linked to another coach")
	ErrRegistrationLinkNotFound  = errors.New("registration link not found")
	ErrAlreadyClientOfCoach      = errors.New("you are already a client of this coach")
	ErrUserExistsUseLogin        = errors.New("an account with this email already exists — please log in instead")
)

// Error implements the error interface for ApiError.
func (e *ApiError) Error_() string {
	if e.Error != nil {
		return fmt.Sprintf("code=%d, err=%v", e.Code, e.Error)
	}
	return fmt.Sprintf("code=%d", e.Code)
}

// NewApiError creates a new ApiError.
func NewApiError(code int, err error) *ApiError {
	return &ApiError{
		Code:  code,
		Error: err,
	}
}
