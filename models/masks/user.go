package masks

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"log"
	"net/http"
	"regexp"
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

	// Masks for user metadata
	UserUpdateMaskUserMetadataHowHeard    = "metadata.how_heard_about_us"
	UserUpdateMaskUserMetadataProfession  = "metadata.profession"
	UserUpdateMaskUserMetadataWorkPlace   = "metadata.work_place"
	UserUpdateMaskUserMetadataClientRange = "metadata.client_range"
	UserUpdateMaskUserMetadataOfferings   = "metadata.offerings"
	UserUpdateMaskUserMetadataSpecialties = "metadata.specialties"
	UserUpdateMaskUserMetadataGoal        = "metadata.goal"
	UserUpdateMaskUserMetadataLevel       = "metadata.level"
	UserUpdateMaskUserMetadataExperience  = "metadata.experience_years"
	UserUpdateMaskUserMetadataCertif      = "metadata.certification"

	// Masks for the private personal details
	UserUpdateMaskUserPersonalBirthDate = "personal.birth_date"
	UserUpdateMaskUserPersonalHeightCm  = "personal.height_cm"
)

func UpdateUserMasks(req *api.RequestUpdateUser) (bson.M, *models.ApiError) {
	// Check if the request is nil
	if req == nil {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrInvalidInputInUpdateMask,
		}
	}

	// "email" and "verification_status" are deliberately not writable here: changing them
	// would let a user re-point the account or self-verify.
	// Create a map to store the fields to be updated
	updateFields := bson.M{}

	// Iterate through the update masks and update the corresponding fields
	for _, mask := range req.UpdateMasks {
		switch mask {
		case UserUpdateMaskUserFirstname:
			updateFields["firstname"] = req.User.UserFirstname

		case UserUpdateMaskUserLastname:
			updateFields["lastname"] = req.User.UserLastname

		case UserUpdateMaskUserRole:
			updateFields["role"] = req.User.UserRole

		case UserUpdateMaskUserGender:
			updateFields["gender"] = req.User.UserGender

		case UserUpdateMaskUserStatus:
			updateFields["status"] = req.User.UserStatus

		case UserUpdateMaskUserProfilePicture:
			updateFields["profile_picture"] = req.User.UserProfilePicture

		case UserUpdateMaskUserDescription:
			updateFields["description"] = req.User.UserDescription

		case UserUpdateMaskUserPhoneNumber:
			updateFields["phone_number"] = req.User.UserPhoneNumber

		case "address.city":
			if req.User.UserAddress.City != nil {
				updateFields["address.city"] = req.User.UserAddress.City
				log.Printf("[DEBUG] address.city updated: %v", req.User.UserAddress.City)
			}

		case "address.country":
			if req.User.UserAddress.Country != nil {
				updateFields["address.country"] = req.User.UserAddress.Country
				log.Printf("[DEBUG] address.country updated: %v", req.User.UserAddress.Country)
			}
		case "address.line1":
			if req.User.UserAddress.Line1 != nil {
				updateFields["address.line1"] = req.User.UserAddress.Line1
				log.Printf("[DEBUG] address.line1 updated: %v", req.User.UserAddress.Line1)
			}
		case "address.line2":
			if req.User.UserAddress.Line2 != nil {
				updateFields["address.line2"] = req.User.UserAddress.Line2
				log.Printf("[DEBUG] address.line2 updated: %v", req.User.UserAddress.Line2)
			}
		case "address.postal_code":
			if req.User.UserAddress.PostalCode != nil {
				updateFields["address.postal_code"] = req.User.UserAddress.PostalCode
				log.Printf("[DEBUG] address.postal_code updated: %v", req.User.UserAddress.PostalCode)
			}
		case "address.state":
			if req.User.UserAddress.State != nil {
				updateFields["address.state"] = req.User.UserAddress.State
				log.Printf("[DEBUG] address.state updated: %v", req.User.UserAddress.State)
			}

			// --- Metadata fields update ---
		case UserUpdateMaskUserMetadataHowHeard:
			// Ensure metadata is not nil before updating the nested field
			if req.User.Metadata != nil {
				updateFields["metadata.how_heard_about_us"] = req.User.Metadata.HowHeardAboutUs
			}

		case UserUpdateMaskUserMetadataProfession:
			if req.User.Metadata != nil {
				updateFields["metadata.profession"] = req.User.Metadata.Profession
			}

		case UserUpdateMaskUserMetadataWorkPlace:
			if req.User.Metadata != nil {
				updateFields["metadata.work_place"] = req.User.Metadata.WorkPlace
			}

		case UserUpdateMaskUserMetadataClientRange:
			if req.User.Metadata != nil {
				updateFields["metadata.client_range"] = req.User.Metadata.ClientRange
			}

		case UserUpdateMaskUserMetadataOfferings:
			if req.User.Metadata != nil {
				updateFields["metadata.offerings"] = req.User.Metadata.Offerings
			}

		case UserUpdateMaskUserMetadataSpecialties:
			if req.User.Metadata != nil {
				specialties, ok := db.NormalizeSpecialties(req.User.Metadata.Specialties)
				if !ok {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidSpecialty}
				}
				updateFields["metadata.specialties"] = specialties
			}

		case UserUpdateMaskUserMetadataGoal:
			if req.User.Metadata != nil {
				if !db.ValidGoal(req.User.Metadata.Goal) {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidGoal}
				}
				updateFields["metadata.goal"] = req.User.Metadata.Goal
			}

		case UserUpdateMaskUserMetadataLevel:
			if req.User.Metadata != nil {
				if !db.ValidLevel(req.User.Metadata.Level) {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidLevel}
				}
				updateFields["metadata.level"] = req.User.Metadata.Level
			}

		case UserUpdateMaskUserMetadataExperience:
			if req.User.Metadata != nil {
				if !db.ValidExperienceYears(req.User.Metadata.ExperienceYears) {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidExperienceYears}
				}
				updateFields["metadata.experience_years"] = req.User.Metadata.ExperienceYears
			}

		case UserUpdateMaskUserMetadataCertif:
			if req.User.Metadata != nil {
				certification, ok := db.NormalizeCertification(req.User.Metadata.Certification)
				if !ok {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidCertification}
				}
				updateFields["metadata.certification"] = certification
			}

		case UserUpdateMaskUserPersonalBirthDate:
			if req.User.Personal != nil {
				if !db.ValidBirthDate(req.User.Personal.BirthDate, time.Now()) {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidBirthDate}
				}
				updateFields["personal.birth_date"] = req.User.Personal.BirthDate
			}

		case UserUpdateMaskUserPersonalHeightCm:
			if req.User.Personal != nil {
				if !db.ValidHeightCm(req.User.Personal.HeightCm) {
					return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidHeight}
				}
				updateFields["personal.height_cm"] = req.User.Personal.HeightCm
			}

		}
	}

	// Return the update fields and no error
	return updateFields, nil
}

// Helper function to compare two addresses
func addressEqual(a, b db.Address) bool {
	// Compare all fields of the address
	return a.City == b.City && a.Country == b.Country && a.Line1 == b.Line1 && a.Line2 == b.Line2 &&
		a.PostalCode == b.PostalCode && a.State == b.State
}

// SearchUserMasks generates dynamic filters for identifier search based on the provided criteria.
func SearchUserMasks(s *db.SearchUser) bson.M {
	filters := bson.M{}

	if s.UserFirstname != "" {
		filters["firstname"] = bson.M{"$regex": safeRegex(s.UserFirstname), "$options": "i"}
	}
	if s.UserLastname != "" {
		filters["lastname"] = bson.M{"$regex": safeRegex(s.UserLastname), "$options": "i"}
	}
	if s.UserEmail != "" {
		filters["email"] = bson.M{"$regex": safeRegex(s.UserEmail), "$options": "i"}
	}
	if s.UserRole != "" {
		filters["role"] = s.UserRole
	}
	if s.UserStatus != "" {
		filters["status"] = s.UserStatus
	}
	if s.UserGender != "" {
		filters["gender"] = s.UserGender
	}
	if s.UserPhoneNumber != "" {
		filters["phone_number"] = bson.M{"$regex": safeRegex(s.UserPhoneNumber), "$options": "i"}
	}
	if s.UserVerificationStatus {
		filters["verification_status"] = s.UserVerificationStatus
	}
	if s.ExternalID != "" {
		filters["externalid"] = s.ExternalID
	}
	if s.UserAddress != nil {
		if s.UserAddress.City != nil {
			filters["address.city"] = bson.M{"$regex": safeRegex(*s.UserAddress.City), "$options": "i"}
		}
		if s.UserAddress.Country != nil {
			filters["address.country"] = bson.M{"$regex": safeRegex(*s.UserAddress.Country), "$options": "i"}
		}
		if s.UserAddress.Line1 != nil {
			filters["address.line1"] = bson.M{"$regex": safeRegex(*s.UserAddress.Line1), "$options": "i"}
		}
		if s.UserAddress.Line2 != nil {
			filters["address.line2"] = bson.M{"$regex": safeRegex(*s.UserAddress.Line2), "$options": "i"}
		}
		if s.UserAddress.PostalCode != nil {
			filters["address.postal_code"] = bson.M{"$regex": safeRegex(*s.UserAddress.PostalCode), "$options": "i"}
		}
		if s.UserAddress.State != nil {
			filters["address.state"] = bson.M{"$regex": safeRegex(*s.UserAddress.State), "$options": "i"}
		}
	}

	return filters
}

// maxSearchLen caps user-supplied search terms before they reach $regex.
const maxSearchLen = 100

// safeRegex caps the length and escapes regex metacharacters so user input matches literally.
func safeRegex(s string) string {
	if len(s) > maxSearchLen {
		s = s[:maxSearchLen]
	}
	return regexp.QuoteMeta(s)
}
