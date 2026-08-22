package domain

import "time"

// Device is a managed-device record normalized before source selection.
type Device struct {
	Source                   string            `cel:"source"                      json:"source"`
	Namespace                string            `cel:"namespace"                   json:"namespace"`
	ID                       string            `cel:"id"                          json:"id"`
	SerialNumber             string            `cel:"serial_number"               json:"serial_number"`
	Name                     string            `cel:"name"                        json:"name"`
	Platform                 string            `cel:"platform"                    json:"platform"`
	PrimaryUserPrincipalName string            `cel:"primary_user_principal_name" json:"primary_user_principal_name"`
	LastContactAt            time.Time         `cel:"last_contact_at"              json:"last_contact_at"`
	EnrolledAt               time.Time         `cel:"enrolled_at"                  json:"enrolled_at"`
	Attributes               map[string]string `cel:"attributes"                  json:"attributes,omitempty"`
}
