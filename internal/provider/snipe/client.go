// Package snipe reads and mutates Snipe-IT through its versioned REST API.
package snipe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	pageSize           = 200
	maxErrorBodyBytes  = 4 << 10
)

// Config contains one Snipe-IT API connection.
type Config struct {
	URL    string
	APIKey string
}

// Client calls a Snipe-IT v1 API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates an authenticated Snipe-IT client.
func NewClient(config Config) (*Client, error) {
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("snipe URL: must be absolute")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("snipe URL: must use HTTP or HTTPS")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("snipe URL: must not contain a query or fragment")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("snipe API key is required")
	}
	return newClient(strings.TrimRight(config.URL, "/"), config.APIKey, &http.Client{Timeout: defaultHTTPTimeout}), nil
}

func newClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: httpClient}
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	payload any,
	successCodes ...int,
) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode Snipe request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestURL := c.baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) != 0 {
		requestURL += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("create Snipe request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Snipe API: %w", err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read Snipe response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close Snipe response: %w", closeErr)
	}
	if slices.Contains(successCodes, response.StatusCode) {
		return responseBody, nil
	}
	return nil, newHTTPError(response.StatusCode, responseBody)
}

func (c *Client) getAll(ctx context.Context, path string, extra url.Values) ([]json.RawMessage, error) {
	rows := make([]json.RawMessage, 0)
	expectedTotal := -1
	for offset := 0; ; {
		query := url.Values{
			"deleted": {"false"},
			"limit":   {strconv.Itoa(pageSize)},
			"offset":  {strconv.Itoa(offset)},
		}
		for key, values := range extra {
			query[key] = append([]string(nil), values...)
		}
		body, err := c.request(ctx, http.MethodGet, path, query, nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var response struct {
			Total *int               `json:"total"`
			Rows  *[]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("decode Snipe %s page: %w", path, err)
		}
		if response.Total == nil || response.Rows == nil {
			return nil, fmt.Errorf("decode Snipe %s page: total and rows are required", path)
		}
		total, pageRows := *response.Total, *response.Rows
		if total < 0 || total < len(rows)+len(pageRows) {
			return nil, fmt.Errorf("snipe %s pagination returned invalid total %d", path, total)
		}
		if expectedTotal >= 0 && total != expectedTotal {
			return nil, fmt.Errorf("snipe %s pagination total changed from %d to %d", path, expectedTotal, total)
		}
		if expectedTotal < 0 {
			expectedTotal = total
		}
		if len(pageRows) == 0 {
			if len(rows) != total {
				return nil, fmt.Errorf("snipe %s pagination ended at %d of %d rows", path, len(rows), total)
			}
			break
		}
		rows = append(rows, pageRows...)
		offset += len(pageRows)
		if offset == total {
			break
		}
	}
	return rows, nil
}

func (c *Client) write(ctx context.Context, method, path string, payload any, label string) ([]byte, error) {
	body, err := c.request(ctx, method, path, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var response struct {
		Status   string          `json:"status"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", label, err)
	}
	if response.Status != "success" {
		return nil, fmt.Errorf("%s failed: %s", label, strings.TrimSpace(string(response.Messages)))
	}
	return body, nil
}

// CreateUser creates an inactive Snipe user and returns its real ID.
func (c *Client) CreateUser(ctx context.Context, patch domain.UserPatch) (int64, error) {
	payload := userPayload(patch)
	password := rand.Text()
	payload["password"] = password
	payload["password_confirmation"] = password
	payload["activated"] = false
	body, err := c.write(ctx, http.MethodPost, "users", payload, "user create")
	if err != nil {
		return 0, err
	}
	var response struct {
		Payload struct {
			ID int64 `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode user create response: %w", err)
	}
	if response.Payload.ID <= 0 {
		return 0, fmt.Errorf("user create succeeded without returning an ID")
	}
	return response.Payload.ID, nil
}

// PatchUser applies a sparse update to one Snipe user.
func (c *Client) PatchUser(ctx context.Context, userID int64, patch domain.UserPatch) error {
	if patch.Empty() {
		return nil
	}
	_, err := c.write(ctx, http.MethodPatch, "users/"+strconv.FormatInt(userID, 10), userPayload(patch), "user patch")
	return err
}

// PatchAsset applies a sparse update to one Snipe asset.
func (c *Client) PatchAsset(ctx context.Context, assetID int64, patch domain.AssetPatch, managedByColumn string) error {
	if patch.Empty() {
		return nil
	}
	payload := make(map[string]any)
	if patch.Name != nil {
		payload["name"] = *patch.Name
	}
	if patch.StatusID != nil {
		payload["status_id"] = *patch.StatusID
	}
	if patch.ManagedBy != nil {
		payload[managedByColumn] = *patch.ManagedBy
	}
	_, err := c.write(ctx, http.MethodPatch, "hardware/"+strconv.FormatInt(assetID, 10), payload, "asset patch")
	return err
}

// CheckinAsset checks in one assigned Snipe asset.
func (c *Client) CheckinAsset(ctx context.Context, assetID int64) error {
	_, err := c.write(ctx, http.MethodPost, "hardware/"+strconv.FormatInt(assetID, 10)+"/checkin", map[string]string{
		"note": "Auto-checkin by SnipeSync",
	}, "asset checkin")
	return err
}

// CheckoutAsset checks one Snipe asset out to a user.
func (c *Client) CheckoutAsset(ctx context.Context, assetID, userID int64, checkoutAt time.Time, location *time.Location) error {
	payload := map[string]any{
		"checkout_to_type": "user",
		"assigned_user":    userID,
		"note":             "Auto-assigned by SnipeSync",
	}
	if !checkoutAt.IsZero() {
		payload["checkout_at"] = checkoutAt.In(location).Format("2006-01-02 15:04:05")
	}
	_, err := c.write(ctx, http.MethodPost, "hardware/"+strconv.FormatInt(assetID, 10)+"/checkout", payload, "asset checkout")
	return err
}

func userPayload(patch domain.UserPatch) map[string]any {
	payload := make(map[string]any)
	if patch.GivenName != nil {
		payload["first_name"] = *patch.GivenName
	}
	if patch.Surname != nil {
		payload["last_name"] = *patch.Surname
	}
	if patch.Username != nil {
		payload["username"] = *patch.Username
	}
	if patch.Email != nil {
		payload["email"] = *patch.Email
	}
	if patch.StartDate != nil {
		payload["start_date"] = *patch.StartDate
	}
	if patch.DepartmentID != nil {
		payload["department_id"] = *patch.DepartmentID
	}
	if patch.LocationID != nil {
		payload["location_id"] = *patch.LocationID
	}
	return payload
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("Snipe API returned HTTP %d", e.status)
	}
	return fmt.Sprintf("Snipe API returned HTTP %d: %s", e.status, e.body)
}

func newHTTPError(status int, body []byte) error {
	if len(body) > maxErrorBodyBytes {
		body = body[:maxErrorBodyBytes]
	}
	return &httpError{status: status, body: strings.TrimSpace(string(body))}
}

func decodeStartDate(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.Split(value, " ")[0]
	}
	var object struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return strings.Split(object.Date, " ")[0]
	}
	return ""
}

func cleanHTML(value string) string {
	return html.UnescapeString(strings.TrimSpace(value))
}
