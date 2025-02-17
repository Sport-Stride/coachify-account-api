package handlers

import (
	"coachify-account-api/models/api"
	"coachify-account-api/models/mapping"
	"coachify-account-api/services"
	"coachify-account-api/utils"

	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func OAuth2Login(authService services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		state, err := utils.GenerateRandomString(32)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err})
			return
		}
		// Store state in secure cookie/redis (with expiry)
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("oauth_state", state, 600, "/", "", true, true)
		providerType := c.Param("provider")
		url := authService.GetOAuth2LoginURL(providerType, state) // Update interface
		c.Redirect(http.StatusTemporaryRedirect, url)
	}
}

func OAuth2Callback(authService services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		providerType := ctx.Param("provider")
		code := ctx.Query("code")
		receivedState := ctx.Query("state")
		if providerType == "" || code == "" || receivedState == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid OAuth callback request"})
			return
		}
		// Retrieve the stored state (e.g., from a cookie or session)
		storedState, err := ctx.Cookie("oauth_state")
		if err != nil || storedState != receivedState {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid state parameter"})
			return
		}
		// Call auth service to handle OAuth logic
		resp, apiErr := authService.HandleOAuthLogin(ctx, providerType, code)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"message": "User logged in successfully",
			"user":    resp.User,
		})
	}
}

func GetUserByEmail(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		entity, err := service.GetUserByEmail(ctx, ctx.Param("prefix"))
		if err != nil {
			ctx.JSON(err.Code, gin.H{"error": err.Error.Error()})
		} else {
			ctx.JSON(200, entity)
		}
	}
}

func GetUserById(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		entity, err := service.GetUserById(ctx, ctx.Param("prefix"))
		if err != nil {
			ctx.JSON(err.Code, gin.H{"error": err.Error.Error()})
		} else {
			ctx.JSON(200, entity)
		}
	}
}

func GetUserByExternalId(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		entity, err := service.GetUserByExternalId(ctx, ctx.Param("prefix"))
		if err != nil {
			ctx.JSON(err.Code, gin.H{"error": err.Error.Error()})
		} else {
			ctx.JSON(200, entity)
		}

	}
}

func Login(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {

		req := new(api.LoginRequest) // Assurez-vous que c'est le bon type
		if err := ctx.ShouldBindJSON(req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		resp, apiErr := wrapper.TryToConnect(ctx, *req)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})

			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "Login successful", "user": resp.User})

	}
}

func Register(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.CreateUserRequest)

		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
			return
		}

		user, apiErr := wrapper.Register(ctx, req)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusCreated, gin.H{"message": "User registered successfully", "user": user})
	}
}

func Confirm(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.ConfirmUserRequest)
		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(
				http.StatusBadRequest,
				gin.H{"error": err.Error()})
			return
		}

		apiErr := wrapper.Confirm(ctx, req)

		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"Email confirmed successfully": true})
	}
}

func ResendConfirmEmail(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.ResendConfirmUserRequest)
		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(
				http.StatusBadRequest,
				gin.H{"error": err.Error()})
			return
		}

		apiErr := wrapper.ResendConfirmEmail(ctx, req.Email)

		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func InitResetPassword(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.ResetPasswordRequest)
		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(
				http.StatusBadRequest,
				gin.H{"error": err.Error()})
			return
		}

		apiErr := wrapper.InitResetPassword(ctx, req)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func ConfirmResetPassword(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.ConfirmResetPasswordRequest)

		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(
				http.StatusBadRequest,
				gin.H{"error": "Invalid input: " + err.Error()})
			return
		}

		apiErr := wrapper.ConfirmResetPassword(ctx, req)

		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return

		}

		ctx.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func RefreshToken(wrapper services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		request := new(api.RequestRefreshtoken)

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		accessToken, err := wrapper.RefreshToken(c, request.Email, request.RefreshToken)

		if err != nil {
			c.JSON(err.Code, gin.H{"error": err.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"access_token": accessToken})
	}
}

func UpdateUser(wrapper services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		req := new(api.RequestUpdateUser)

		if err := c.ShouldBindJSON(req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
			return
		}

		userUpdated, apiErr := wrapper.UpdateUser(c.Request.Context(), *req)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, userUpdated)
	}
}
func GetAllUsersPag(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Parse the request body into a SearchUser struct
		var searchQuery api.SearchUser
		if err := ctx.ShouldBindJSON(&searchQuery); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		// Call the service to retrieve paginated users
		data, count, err := service.GetAllUsersPag(searchQuery)
		if err != nil {
			ctx.JSON(err.Code, gin.H{"error": err.Error})
			return
		}

		// Calculate pagination details
		pageNbr := searchQuery.Page
		sizeNbr := searchQuery.Size

		// Ensure sizeNbr is at least 1 to avoid division by zero
		if sizeNbr < 1 {
			sizeNbr = 1
		}

		// Calculate total pages
		totalPages := 0
		if count > 0 {
			totalPages = (count + sizeNbr - 1) / sizeNbr
		}

		// Prepare the response
		dataResponse := &mapping.PaginatedUser{
			Users:        data,
			Page:         pageNbr,
			TotalPerPage: len(data),
			Total:        count,
			TotalPages:   totalPages,
		}

		ctx.JSON(http.StatusOK, dataResponse)
	}
}

// Helper function to parse optional boolean query parameters
func getOptionalBoolQuery(ctx *gin.Context, key string) *bool {
	value := ctx.DefaultQuery(key, "")
	if value == "" {
		return nil
	}
	parsedValue, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	return &parsedValue
}

func DeleteUser(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.DeleteUserRequest)

		if err := ctx.ShouldBindJSON(req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
			return
		}

		apiErr := service.DeleteUser(ctx, req.ExternalID)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "User deleted successfully"})

	}
}

func AddUser(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.CreateUserRequest)

		err := ctx.ShouldBindJSON(req)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
			return
		}

		user, apiErr := service.AddUser(ctx, req)
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		ctx.JSON(http.StatusCreated, gin.H{"message": "user created ", "user": user})
	}
}

func getOptionalQueryParam(ctx *gin.Context, param string) *string {
	value := ctx.DefaultQuery(param, "")
	if value != "" {
		return &value
	}
	return nil
}
