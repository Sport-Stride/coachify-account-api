package db

import "github.com/spf13/viper"

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

// IsInactive reports statuses that must not authenticate (blocked, suspended, deactivated).
func (s UserStatus) IsInactive() bool {
	return s == Blocked || s == Suspended || s == Deactivated
}

// RoleGymOwner is a business owner above head coaches. It is not a coach: it never goes through
// signup, trial or invitation flows and is created only by an admin.
const RoleGymOwner = "gym_owner"

// GymOwnerAsCoachFlag is the Viper key that lets a gym_owner use coaching functionality (default false).
const GymOwnerAsCoachFlag = "GYM_OWNER_AS_COACH"

// IsCoachingRole reports roles that may use coaching functionality for their own clients, plans, invitations
// and entitlements: coach always, gym_owner only while GYM_OWNER_AS_COACH is on. It never grants access to other
// people's data and must not gate gym membership or head-coach flows.
func IsCoachingRole(role string) bool {
	return role == "coach" || (role == RoleGymOwner && viper.GetBool(GymOwnerAsCoachFlag))
}

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

// IsMatriculeRole reports roles that may hold a matricule fiscale: coaching roles and nutritionist.
func IsMatriculeRole(role string) bool {
	return IsCoachingRole(role) || role == "nutritionist"
}
