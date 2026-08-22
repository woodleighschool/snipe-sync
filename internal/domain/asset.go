package domain

// Asset is the current Snipe-IT state used by policy and planning.
type Asset struct {
	ID             int64             `cel:"id"              json:"id"`
	AssetTag       string            `cel:"asset_tag"       json:"asset_tag"`
	SerialNumber   string            `cel:"serial_number"   json:"serial_number"`
	Name           string            `cel:"name"            json:"name"`
	Manufacturer   string            `cel:"manufacturer"    json:"manufacturer"`
	Status         string            `cel:"status"          json:"status"`
	Archived       bool              `cel:"archived"        json:"archived"`
	AssignedToID   int64             `cel:"assigned_to_id"  json:"assigned_to_id,omitempty"`
	AssignedToType string            `cel:"assigned_to_type" json:"assigned_to_type,omitempty"`
	AssignedEmail  string            `cel:"assigned_email"  json:"assigned_email,omitempty"`
	ManagedBy      string            `cel:"managed_by"      json:"managed_by,omitempty"`
	CustomFields   map[string]string `cel:"custom_fields"   json:"custom_fields,omitempty"`
	StatusID       int64             `cel:"status_id"       json:"status_id"`
}

// TargetUser is the current Snipe-IT user state used by the planner.
type TargetUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	GivenName    string `json:"given_name"`
	Surname      string `json:"surname"`
	StartDate    string `json:"start_date"`
	DepartmentID int64  `json:"department_id,omitempty"`
	LocationID   int64  `json:"location_id,omitempty"`
}

// AssetPatch contains only Snipe asset fields that should be written.
type AssetPatch struct {
	Name      *string `json:"name,omitempty"`
	StatusID  *int64  `json:"status_id,omitempty"`
	ManagedBy *string `json:"managed_by,omitempty"`
}

// Empty reports whether the patch contains no fields.
func (p AssetPatch) Empty() bool {
	return p == AssetPatch{}
}
