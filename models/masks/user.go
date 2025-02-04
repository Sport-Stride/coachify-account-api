package masks

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

const (
	UserUpdateMaskUserFirstname          = "firstname"
	UserUpdateMaskUserLastname           = "lastname"
	UserUpdateMaskUserEmail              = "email"
	UserUpdateMaskUserRole               = "role"
	UserUpdateMaskUserGender             = "gender"
	UserUpdateMaskUserStatus             = "status"
	UserUpdateMaskUserProfilePicture     = "profile_picture"
	UserUpdateMaskUserDescription        = "description"
	UserUpdateMaskUserPhoneNumber        = "phone_number"
	UserUpdateMaskUserAddress            = "address"
	UserUpdateMaskUserVerificationStatus = "verification_status"
)

func UpdateUserMasks(dataDB *db.User, dataReq *api.RequestUpdateUser) (*db.User, *models.ApiError) {
	// Check if both input parameters are nil
	if (dataDB == nil) || (dataReq == nil) {
		// Return an error if both inputs are nil
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInvalidInputInUpdateMask,
		}
	}
	// Map the API identifier data to the DB identifier format if only dataReq is provided
	*dataDB = mapping.UpdateUserAPIToDB(dataReq.User)

	now := time.Now() // Store the current time for tracking updates
	var updated bool  // Track if any field has been updated

	// Iterate through the update masks and update the corresponding fields
	for _, s := range dataReq.UpdateMasks {
		switch s {

		case UserUpdateMaskUserFirstname:
			// Update the first name if it has changed
			if dataDB.UserFirstname != dataReq.User.UserFirstname {
				dataDB.UserFirstname = dataReq.User.UserFirstname
				updated = true
			}

		case UserUpdateMaskUserLastname:
			// Update the last name if it has changed
			if dataDB.UserLastname != dataReq.User.UserLastname {
				dataDB.UserLastname = dataReq.User.UserLastname
				updated = true
			}

		case UserUpdateMaskUserEmail:
			// Update the email if it has changed
			if dataDB.UserEmail != dataReq.User.UserEmail {
				dataDB.UserEmail = dataReq.User.UserEmail
				updated = true
			}

		case UserUpdateMaskUserRole:
			// Update the identifier role if it has changed
			if dataDB.UserRole != dataReq.User.UserRole {
				dataDB.UserRole = dataReq.User.UserRole
				updated = true
			}

		case UserUpdateMaskUserGender:
			// Update the gender if it has changed
			if string(dataDB.UserGender) != dataReq.User.UserGender {
				dataDB.UserGender = db.UserGender(dataReq.User.UserGender)
				updated = true
			}

		case UserUpdateMaskUserStatus:
			// Update the identifier status if it has changed
			if string(dataDB.UserStatus) != dataReq.User.UserStatus {
				dataDB.UserStatus = db.UserStatus(dataReq.User.UserStatus)
				updated = true
			}

		case UserUpdateMaskUserProfilePicture:
			// Update the profile picture if it has changed
			if dataDB.UserProfilePicture != dataReq.User.UserProfilePicture {
				dataDB.UserProfilePicture = dataReq.User.UserProfilePicture
				updated = true
			}

		case UserUpdateMaskUserDescription:
			// Update the description if it has changed
			if dataDB.UserDescription != dataReq.User.UserDescription {
				dataDB.UserDescription = dataReq.User.UserDescription
				updated = true
			}

		case UserUpdateMaskUserPhoneNumber:
			// Update the phone number if it has changed
			if dataDB.UserPhoneNumber != dataReq.User.UserPhoneNumber {
				dataDB.UserPhoneNumber = dataReq.User.UserPhoneNumber
				updated = true
			}

		case UserUpdateMaskUserAddress:
			// Update the address if it has changed
			if dataDB.UserAddress.Line1 != dataReq.User.UserAddress.Line1 ||
				dataDB.UserAddress.Line2 != dataReq.User.UserAddress.Line2 ||
				dataDB.UserAddress.City != dataReq.User.UserAddress.City ||
				dataDB.UserAddress.State != dataReq.User.UserAddress.State ||
				dataDB.UserAddress.Country != dataReq.User.UserAddress.Country ||
				dataDB.UserAddress.PostalCode != dataReq.User.UserAddress.PostalCode {
				dataDB.UserAddress = db.Address(dataReq.User.UserAddress)
				updated = true
			}

		case UserUpdateMaskUserVerificationStatus:
			// Update the verification status if it has changed
			if dataDB.UserVerificationStatus != dataReq.User.VerificationStatus {
				dataDB.UserVerificationStatus = dataReq.User.VerificationStatus
				updated = true
			}

		}
	}

	// If any field was updated, update the 'UserUpdatedAt' timestamp
	if updated {
		dataDB.UserUpdatedAt = now
	}

	// Return the updated identifier data and no error
	return dataDB, nil
}

// Helper function to compare two addresses
func addressEqual(a, b db.Address) bool {
	// Compare all fields of the address
	return a.City == b.City && a.Country == b.Country && a.Line1 == b.Line1 && a.Line2 == b.Line2 &&
		a.PostalCode == b.PostalCode && a.State == b.State
}

// SearchUserMasks generates dynamic filters for identifier search based on the provided criteria.
func SearchUserMasks(search *db.SearchUser) bson.M {
	filters := bson.M{}

	if search.ExternalID != "" {
		filters["external_id"] = search.ExternalID
	}
	if search.UserFirstname != "" {
		filters["firstname"] = search.UserFirstname
	}
	if search.UserLastname != "" {
		filters["lastname"] = search.UserLastname
	}
	if search.UserEmail != "" {
		filters["email"] = search.UserEmail
	}
	if search.UserRole != "" {
		filters["role"] = search.UserRole
	}
	if search.UserStatus != "" {
		filters["status"] = search.UserStatus
	}
	if search.UserGender != "" {
		filters["gender"] = search.UserGender
	}
	if search.UserPhoneNumber != "" {
		filters["phone_number"] = search.UserPhoneNumber
	}
	if search.UserAddress != nil {
		// Apply filters for individual address fields
		if search.UserAddress.City != nil {
			filters["address.city"] = *search.UserAddress.City
		}
		if search.UserAddress.Country != nil {
			filters["address.country"] = *search.UserAddress.Country
		}
		if search.UserAddress.Line1 != nil {
			filters["address.line1"] = *search.UserAddress.Line1
		}
		if search.UserAddress.Line2 != nil {
			filters["address.line2"] = *search.UserAddress.Line2
		}
		if search.UserAddress.PostalCode != nil {
			filters["address.postal_code"] = *search.UserAddress.PostalCode
		}
		if search.UserAddress.State != nil {
			filters["address.state"] = *search.UserAddress.State
		}
	}
	if search.VerificationStatusSet {
		filters["verification_status"] = search.UserVerificationStatus
	}
	if !search.UserCreatedAt.IsZero() {
		filters["created_at"] = search.UserCreatedAt
	}
	if !search.UserUpdatedAt.IsZero() {
		filters["updated_at"] = search.UserUpdatedAt
	}
	if !search.UserLastLogin.IsZero() {
		filters["last_login"] = search.UserLastLogin
	}

	return filters
}
