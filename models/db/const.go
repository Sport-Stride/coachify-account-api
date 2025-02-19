package db

type UserStatus string

const (
	Active      UserStatus = "Active"
	Inactive    UserStatus = "Inactive"
	Suspended   UserStatus = "Suspended"
	ComReg1     UserStatus = "Complete-registration-1"
	ComReg2     UserStatus = "Complete-registration-2"
	ComReg3     UserStatus = "Complete-registration-3"
	Deactivated UserStatus = "Deactivated"
	ToConfirm   UserStatus = "To Confirm"
	Blocked     UserStatus = "Blocked"
)

type UserGender string

const (
	Male   UserGender = "Male"
	Female UserGender = "Female"
)
