package api

type UserStatus string

const (
	Active      UserStatus = "Active"
	Inactive    UserStatus = "Inactive"
	Suspended   UserStatus = "Suspended"
	Deactivated UserStatus = "Deactivated"
	ToConfirm   UserStatus = "To Confirm"
	Blocked     UserStatus = "Blocked"
)

type ClientType string

const (
	Influencer ClientType = "influencer"
	Agency     ClientType = "agency"
	Shop       ClientType = "shop"
	Restaurant ClientType = "restaurant"
	Brand      ClientType = "brand"
)

type UserGender string

const (
	Male   UserGender = "Male"
	Female UserGender = "Female"
)
