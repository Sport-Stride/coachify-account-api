package masks

import (
	"strings"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func sp(s string) *string { return &s }

func TestUpdateUserMasks_NilRequest(t *testing.T) {
	f, apiErr := UpdateUserMasks(nil)
	assert.Nil(t, f)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrInvalidInputInUpdateMask)
}

func TestUpdateUserMasks_ScalarFields(t *testing.T) {
	req := &api.RequestUpdateUser{
		User: api.UserRequest{
			UserFirstname: "A", UserLastname: "B", UserRole: "coach",
			UserGender: "female", UserStatus: "Active", UserProfilePicture: "pic", UserDescription: "d",
			UserPhoneNumber: "123",
		},
		UpdateMasks: []string{"firstname", "lastname", "role", "gender", "status",
			"profile_picture", "description", "phone_number"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{
		"firstname": "A", "lastname": "B", "role": "coach", "gender": "female",
		"status": "Active", "profile_picture": "pic", "description": "d", "phone_number": "123",
	}, f)
}

func TestUpdateUserMasks_OnlyMaskedFieldsAndUnknownIgnored(t *testing.T) {
	req := &api.RequestUpdateUser{
		User:        api.UserRequest{UserFirstname: "A", UserLastname: "B", UserRole: "admin"},
		UpdateMasks: []string{"firstname", "password", "external_id", "$set", "providers"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{"firstname": "A"}, f, "unmasked and unknown keys must not reach the update")
}

func TestUpdateUserMasks_EmptyMasks(t *testing.T) {
	f, apiErr := UpdateUserMasks(&api.RequestUpdateUser{User: api.UserRequest{UserFirstname: "A"}})
	require.Nil(t, apiErr)
	assert.Empty(t, f)
}

func TestUpdateUserMasks_Address(t *testing.T) {
	all := []string{"address.city", "address.country", "address.line1", "address.line2", "address.postal_code", "address.state"}

	t.Run("nil address fields are skipped", func(t *testing.T) {
		f, apiErr := UpdateUserMasks(&api.RequestUpdateUser{UpdateMasks: all})
		require.Nil(t, apiErr)
		assert.Empty(t, f)
	})
	t.Run("set address fields are updated", func(t *testing.T) {
		req := &api.RequestUpdateUser{
			User: api.UserRequest{UserAddress: api.Address{
				City: sp("Tunis"), Country: sp("TN"), Line1: sp("l1"), Line2: sp("l2"), PostalCode: sp("1000"), State: sp("S"),
			}},
			UpdateMasks: all,
		}
		f, apiErr := UpdateUserMasks(req)
		require.Nil(t, apiErr)
		assert.Len(t, f, 6)
		assert.Equal(t, req.User.UserAddress.City, f["address.city"])
		assert.Equal(t, req.User.UserAddress.PostalCode, f["address.postal_code"])
	})
}

func TestUpdateUserMasks_Metadata(t *testing.T) {
	all := []string{UserUpdateMaskUserMetadataHowHeard, UserUpdateMaskUserMetadataProfession,
		UserUpdateMaskUserMetadataWorkPlace, UserUpdateMaskUserMetadataClientRange, UserUpdateMaskUserMetadataOfferings}

	t.Run("nil metadata does not panic and updates nothing", func(t *testing.T) {
		var f bson.M
		var apiErr *models.ApiError
		require.NotPanics(t, func() {
			f, apiErr = UpdateUserMasks(&api.RequestUpdateUser{UpdateMasks: all})
		})
		require.Nil(t, apiErr)
		assert.Empty(t, f)
	})
	t.Run("populated metadata", func(t *testing.T) {
		req := &api.RequestUpdateUser{
			User: api.UserRequest{Metadata: &db.UserMetadata{
				HowHeardAboutUs: "friend", Profession: "coach", WorkPlace: "gym", ClientRange: "10-20", Offerings: []string{"a", "b"},
			}},
			UpdateMasks: all,
		}
		f, apiErr := UpdateUserMasks(req)
		require.Nil(t, apiErr)
		assert.Equal(t, bson.M{
			"metadata.how_heard_about_us": "friend", "metadata.profession": "coach",
			"metadata.work_place": "gym", "metadata.client_range": "10-20", "metadata.offerings": []string{"a", "b"},
		}, f)
	})
}

func TestAddressEqual(t *testing.T) {
	a := db.Address{City: sp("x")}
	assert.True(t, addressEqual(a, a))
	assert.True(t, addressEqual(db.Address{}, db.Address{}))
	assert.False(t, addressEqual(a, db.Address{}))
	assert.False(t, addressEqual(db.Address{Country: sp("a")}, db.Address{Country: sp("b")}))
	// Compares pointers, so equal values behind different pointers differ.
	assert.False(t, addressEqual(db.Address{City: sp("x")}, db.Address{City: sp("x")}))
}

func TestSearchUserMasks(t *testing.T) {
	t.Run("empty search yields empty filter", func(t *testing.T) {
		assert.Empty(t, SearchUserMasks(&db.SearchUser{}))
	})
	t.Run("text fields are case-insensitive regex; exact fields are equality", func(t *testing.T) {
		f := SearchUserMasks(&db.SearchUser{
			UserFirstname: "ad", UserLastname: "lo", UserEmail: "@x", UserPhoneNumber: "55",
			UserRole: "coach", UserStatus: "Active", UserGender: "female", ExternalID: "e1",
			UserVerificationStatus: true,
		})
		for _, k := range []string{"firstname", "lastname", "email", "phone_number"} {
			m, ok := f[k].(bson.M)
			require.True(t, ok, k)
			assert.Equal(t, "i", m["$options"])
		}
		assert.Equal(t, "coach", f["role"])
		assert.Equal(t, db.UserStatus("Active"), f["status"])
		assert.Equal(t, "e1", f["externalid"])
		assert.Equal(t, true, f["verification_status"])
	})
	t.Run("nil address is safe", func(t *testing.T) {
		assert.NotPanics(t, func() { SearchUserMasks(&db.SearchUser{UserAddress: nil}) })
	})
	t.Run("address filters only for set fields", func(t *testing.T) {
		f := SearchUserMasks(&db.SearchUser{UserAddress: &db.Address{City: sp("Tu"), State: sp("S")}})
		assert.Len(t, f, 2)
		assert.Contains(t, f, "address.city")
		assert.Contains(t, f, "address.state")
	})
	t.Run("all address filters", func(t *testing.T) {
		f := SearchUserMasks(&db.SearchUser{UserAddress: &db.Address{
			City: sp("a"), Country: sp("b"), Line1: sp("c"), Line2: sp("d"), PostalCode: sp("e"), State: sp("f"),
		}})
		assert.Len(t, f, 6)
	})
}

func TestSearchUserMasks_RegexEscapedAndCapped(t *testing.T) {
	f := SearchUserMasks(&db.SearchUser{UserFirstname: ".*", UserEmail: "(a+)+$", UserAddress: &db.Address{City: sp("a.b")}})
	assert.Equal(t, `\.\*`, f["firstname"].(bson.M)["$regex"])
	assert.Equal(t, `\(a\+\)\+\$`, f["email"].(bson.M)["$regex"])
	assert.Equal(t, `a\.b`, f["address.city"].(bson.M)["$regex"])

	long := strings.Repeat("a", 500)
	got := SearchUserMasks(&db.SearchUser{UserLastname: long})["lastname"].(bson.M)["$regex"].(string)
	assert.Len(t, got, 100)
}

func TestUpdateUserMasks_IgnoresEmailAndVerificationStatus(t *testing.T) {
	req := &api.RequestUpdateUser{
		User:        api.UserRequest{UserFirstname: "A", UserEmail: "attacker@x.y", VerificationStatus: true},
		UpdateMasks: []string{"email", "verification_status", "firstname"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{"firstname": "A"}, f)
}

func TestUpdateUserMasks_OnboardingFields(t *testing.T) {
	req := &api.RequestUpdateUser{
		User: api.UserRequest{Metadata: &db.UserMetadata{
			Specialties: []string{"running", "yoga", "running"}, Goal: "build_muscle", Level: "beginner",
		}},
		UpdateMasks: []string{"metadata.specialties", "metadata.goal", "metadata.level"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{
		"metadata.specialties": []string{"running", "yoga"},
		"metadata.goal":        "build_muscle",
		"metadata.level":       "beginner",
	}, f)
}

func TestUpdateUserMasks_OnboardingFieldsClear(t *testing.T) {
	req := &api.RequestUpdateUser{
		User:        api.UserRequest{Metadata: &db.UserMetadata{}},
		UpdateMasks: []string{"metadata.specialties", "metadata.goal", "metadata.level"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{"metadata.specialties": []string{}, "metadata.goal": "", "metadata.level": ""}, f)
}

func TestUpdateUserMasks_OnboardingFieldsRejectUnknownValues(t *testing.T) {
	cases := []struct {
		mask string
		meta db.UserMetadata
		err  error
	}{
		{"metadata.specialties", db.UserMetadata{Specialties: []string{"strength", "crossfit"}}, models.ErrInvalidSpecialty},
		{"metadata.goal", db.UserMetadata{Goal: "Perdre du poids"}, models.ErrInvalidGoal},
		{"metadata.level", db.UserMetadata{Level: "2"}, models.ErrInvalidLevel},
	}
	for _, c := range cases {
		meta := c.meta
		f, apiErr := UpdateUserMasks(&api.RequestUpdateUser{User: api.UserRequest{Metadata: &meta}, UpdateMasks: []string{c.mask}})
		assert.Nil(t, f, c.mask)
		require.NotNil(t, apiErr, c.mask)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code, c.mask)
		assert.ErrorIs(t, apiErr.Error, c.err, c.mask)
	}
}

func TestUpdateUserMasks_OnboardingFieldsWithoutMetadataIgnored(t *testing.T) {
	f, apiErr := UpdateUserMasks(&api.RequestUpdateUser{UpdateMasks: []string{"metadata.specialties", "metadata.goal", "metadata.level"}})
	require.Nil(t, apiErr)
	assert.Empty(t, f)
}

func TestUpdateUserMasks_PersonalFields(t *testing.T) {
	req := &api.RequestUpdateUser{
		User:        api.UserRequest{Personal: &db.PersonalInfo{BirthDate: "1995-04-12", HeightCm: 172}},
		UpdateMasks: []string{"personal.birth_date", "personal.height_cm"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{"personal.birth_date": "1995-04-12", "personal.height_cm": 172}, f)
}

func TestUpdateUserMasks_PersonalFieldsRejectInvalid(t *testing.T) {
	cases := []struct {
		mask     string
		personal db.PersonalInfo
		err      error
	}{
		{"personal.birth_date", db.PersonalInfo{BirthDate: "12/04/1995"}, models.ErrInvalidBirthDate},
		{"personal.birth_date", db.PersonalInfo{BirthDate: "2999-01-01"}, models.ErrInvalidBirthDate},
		{"personal.height_cm", db.PersonalInfo{HeightCm: 400}, models.ErrInvalidHeight},
	}
	for _, c := range cases {
		p := c.personal
		f, apiErr := UpdateUserMasks(&api.RequestUpdateUser{User: api.UserRequest{Personal: &p}, UpdateMasks: []string{c.mask}})
		assert.Nil(t, f, c.mask)
		require.NotNil(t, apiErr, c.mask)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code, c.mask)
		assert.ErrorIs(t, apiErr.Error, c.err, c.mask)
	}
}
