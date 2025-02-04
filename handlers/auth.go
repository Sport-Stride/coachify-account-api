package handlers

import (
	"coachify-account-api/models/api"
	"coachify-account-api/models/mapping"
	"coachify-account-api/services"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

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

		ctx.JSON(http.StatusCreated, gin.H{"message": "Register successful", "user": user})
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

		ctx.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func ResendConfirmEmail(wrapper services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := new(api.ResendConfirmUserRequest)
		err := ctx.ShouldBind(req)
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
		err := ctx.ShouldBind(req)
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

		userID := c.Param("prefix")
		if userID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing or invalid user ID"})
			return
		}

		userUpdated, apiErr := wrapper.UpdateUser(c.Request.Context(), userID, *req)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, userUpdated)
	}
}

func GetAllUsersPag(service services.AuthService) gin.HandlerFunc {

	return func(ctx *gin.Context) {
		verificationStatusStr := ctx.DefaultQuery("verification_status", "")
		verificationStatus := false
		verificationStatusSet := false

		if verificationStatusStr != "" {
			parsedVerificationStatus, err := strconv.ParseBool(verificationStatusStr)
			if err != nil {
				ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid value for 'verification_status'. Must be true or false."})
				return
			}
			verificationStatus = parsedVerificationStatus
			verificationStatusSet = true
		}

		searchQuery := &api.SearchUser{
			Firstname:             ctx.DefaultQuery("firstname", ""),
			Lastname:              ctx.DefaultQuery("lastname", ""),
			Email:                 ctx.DefaultQuery("email", ""),
			Role:                  ctx.DefaultQuery("role", ""),
			Status:                ctx.DefaultQuery("status", ""),
			Gender:                ctx.DefaultQuery("gender", ""),
			PhoneNumber:           ctx.DefaultQuery("phone_number", ""),
			VerificationStatus:    verificationStatus,
			VerificationStatusSet: verificationStatusSet,
			ExternalID:            ctx.DefaultQuery("external_id", ""),
			Page:                  ctx.DefaultQuery("page", "1"),
			Size:                  ctx.DefaultQuery("size", "10"),
		}

		searchQuery.Address = &api.Address{
			City:       getOptionalQueryParam(ctx, "address.city"),
			Country:    getOptionalQueryParam(ctx, "address.country"),
			Line1:      getOptionalQueryParam(ctx, "address.line1"),
			Line2:      getOptionalQueryParam(ctx, "address.line2"),
			PostalCode: getOptionalQueryParam(ctx, "address.postal_code"),
			State:      getOptionalQueryParam(ctx, "address.state"),
		}

		pageNbr, err := strconv.Atoi(searchQuery.Page)
		if err != nil {
			pageNbr = 1 // Default value in case of error
		}
		sizeNbr, err := strconv.Atoi(searchQuery.Size)
		if err != nil {
			sizeNbr = 10 // Default value in case of error
		}

		data, count, err := service.GetAllUsersPag(*searchQuery)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		totalPages := (count + sizeNbr - 1) / sizeNbr // Calculate the total number of pages

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

func DeleteUser(service services.AuthService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		apiErr := service.DeleteUser(ctx, ctx.Param("prefix"))
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
