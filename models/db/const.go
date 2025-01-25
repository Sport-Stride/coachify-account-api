package db

type UserStatus string

const (
	Active      UserStatus = "Active"
	Inactive    UserStatus = "Inactive"
	Suspended   UserStatus = "Suspended"
	Deactivated UserStatus = "Deactivated"
	ToConfirm   UserStatus = "To Confirm"
	Blocked     UserStatus = "Blocked"
)

type UserGender string

const (
	Male   UserGender = "Male"
	Female UserGender = "Female"
)
