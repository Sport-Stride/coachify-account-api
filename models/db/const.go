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

// RoleGymOwner is a business owner above head coaches. It is not a coach: it never goes through
// signup, trial or invitation flows and is created only by an admin.
const RoleGymOwner = "gym_owner"

type UserGender string

const (
	Male   UserGender = "Male"
	Female UserGender = "Female"
)

type MatriculeFiscaleStatus string

const (
	MatriculeFiscaleNone     MatriculeFiscaleStatus = "none"
	MatriculeFiscalePending  MatriculeFiscaleStatus = "pending"
	MatriculeFiscaleApproved MatriculeFiscaleStatus = "approved"
	MatriculeFiscaleRejected MatriculeFiscaleStatus = "rejected"
)
