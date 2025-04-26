package mapping

import (
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"time"
)

// ConvertClientFilters converts an API ClientFilters struct to a DB ClientFilters struct.
func ConvertClientFilters(apiFilters api.ClientFilters) db.ClientFilters {
	return db.ClientFilters{
		Status:      apiFilters.Status,
		JoinedAfter: apiFilters.JoinedAfter,
	}
}

// SearchClientAPIToDB maps the API SearchClient request to a DB SearchClient model.
func SearchClientAPIToDB(search api.SearchClient) db.SearchClient {
	return db.SearchClient{
		Page:    search.Page,
		Size:    search.Size,
		Query:   search.Query,
		Filters: ConvertClientFilters(search.Filters),
	}
}

// ToClientResponse converts a DB user to an API ClientResponse.
// (Adjust the field names as per your actual db.User definition.)
func ToClientResponse(user db.UserResponse) api.ClientResponse {
	return api.ClientResponse{
		ID:          user.ExternalID,
		Firstname:   user.Firstname,
		Lastname:    user.Lastname,
		Email:       user.Email,
		Phone:       user.PhoneNumber,
		JoinedAt:    user.CreatedAt,
		LastSession: user.LastLogin,
		//Progress:    user.Metadata.Progress,
	}
}

// func MapCoachClientInvitation(coachID, clientID string) *db.CoachClient {
// 	now := time.Now()
// 	return &db.CoachClient{
// 		CoachID:   coachID,
// 		ClientID:  clientID,
// 		Status:    "invited",
// 		InvitedAt: now,
// 		CreatedAt: now,
// 		UpdatedAt: now,
// 	}
// }

// InviteToCreateUserRequest maps an InviteClientRequest to a CreateUserRequest.
func InviteToCreateUserRequest(req *api.CreateUserRequest, encryptedPassword string, id string, confirmCode db.UserConfirmCode) *db.User {
	now := time.Now()
	return &db.User{
		ExternalID:      id,
		UserConfirmCode: &confirmCode,
		UserEmail:       req.Email,
		UserPassword:    encryptedPassword,
		UserRole:        "user", // statically set role for invited users
		UserCreatedAt:   now,
		UserUpdatedAt:   now,
		UserStatus:      db.UserStatus(req.Status),
	}
}
