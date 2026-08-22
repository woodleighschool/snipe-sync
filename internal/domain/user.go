// Package domain defines provider-neutral reconciliation records.
package domain

import "time"

// User is an Entra identity normalized for policy evaluation and planning.
type User struct {
	Present           bool              `cel:"present"             json:"present"`
	ID                string            `cel:"id"                  json:"id"`
	GivenName         string            `cel:"given_name"          json:"given_name"`
	Surname           string            `cel:"surname"             json:"surname"`
	MailNickname      string            `cel:"mail_nickname"       json:"mail_nickname"`
	UserPrincipalName string            `cel:"user_principal_name" json:"user_principal_name"`
	Department        string            `cel:"department"          json:"department"`
	CreatedAt         time.Time         `cel:"created_at"           json:"created_at"`
	Groups            []string          `cel:"groups"              json:"groups,omitempty"`
	Attributes        map[string]string `cel:"attributes"          json:"attributes,omitempty"`
	GroupsComplete    bool              `cel:"groups_complete"     json:"groups_complete"`
}

// UserPatch contains only Snipe user fields that should be written.
type UserPatch struct {
	GivenName    *string `json:"given_name,omitempty"`
	Surname      *string `json:"surname,omitempty"`
	Username     *string `json:"username,omitempty"`
	Email        *string `json:"email,omitempty"`
	StartDate    *string `json:"start_date,omitempty"`
	DepartmentID *int64  `json:"department_id,omitempty"`
	LocationID   *int64  `json:"location_id,omitempty"`
}

// Empty reports whether the patch contains no fields.
func (p UserPatch) Empty() bool {
	return p == UserPatch{}
}
