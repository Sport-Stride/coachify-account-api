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
			UserFirstname: "A", UserLastname: "B", UserEmail: "a@b.c", UserRole: "coach",
			UserGender: "female", UserStatus: "Active", UserProfilePicture: "pic", UserDescription: "d",
			UserPhoneNumber: "123", VerificationStatus: true,
		},
		UpdateMasks: []string{"firstname", "lastname", "email", "role", "gender", "status",
			"profile_picture", "description", "phone_number", "verification_status"},
	}
	f, apiErr := UpdateUserMasks(req)
	require.Nil(t, apiErr)
	assert.Equal(t, bson.M{
		"firstname": "A", "lastname": "B", "email": "a@b.c", "role": "coach", "gender": "female",
		"status": "Active", "profile_picture": "pic", "description": "d", "phone_number": "123",
		"verification_status": true,
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
