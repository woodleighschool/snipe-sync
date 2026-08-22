// Package jamf reads managed-device inventory from the Jamf Pro JSON API.
package jamf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	tokenRefreshLeeway = 30 * time.Second
)

// Config contains one Jamf Pro API client-credentials connection.
type Config struct {
	URL          string
	ClientID     string
	ClientSecret string
}

// Client calls the modern Jamf Pro JSON API.
type Client struct {
	baseURL      string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	now          func() time.Time

	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

// NewClient creates a Jamf Pro client with an in-memory OAuth token cache.
func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf("client ID and client secret are required")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("jamf URL: must be absolute")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("jamf URL: must use HTTP or HTTPS")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("jamf URL: must not contain a query or fragment")
	}

	return newClient(
		strings.TrimRight(config.URL, "/"),
		config.ClientID,
		config.ClientSecret,
		&http.Client{Timeout: defaultHTTPTimeout},
		time.Now,
	), nil
}

func newClient(
	baseURL string,
	clientID string,
	clientSecret string,
	httpClient *http.Client,
	now func() time.Time,
) *Client {
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   httpClient,
		now:          now,
	}
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	payload any,
	successCodes ...int,
) ([]byte, error) {
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, err
	}
	for attempt := range 2 {
		token, tokenErr := c.token(ctx)
		if tokenErr != nil {
			return nil, tokenErr
		}
		requestURL := c.baseURL + path
		if len(query) != 0 {
			requestURL += "?" + query.Encode()
		}
		request, requestErr := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(encoded))
		if requestErr != nil {
			return nil, fmt.Errorf("create Jamf request: %w", requestErr)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, requestErr := c.httpClient.Do(request)
		if requestErr != nil {
			return nil, fmt.Errorf("call Jamf API: %w", requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Jamf response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Jamf response: %w", closeErr)
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.invalidateToken(token)
			continue
		}
		if !containsStatus(successCodes, response.StatusCode) {
			return nil, newHTTPError(response.StatusCode, body)
		}
		return body, nil
	}
	return nil, fmt.Errorf("jamf authentication failed after token refresh")
}

func (c *Client) token(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.accessToken != "" && c.now().Add(tokenRefreshLeeway).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v1/oauth/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("create Jamf token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request Jamf token: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read Jamf token error: %w", readErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close Jamf token error response: %w", closeErr)
		}
		return "", fmt.Errorf("request Jamf token: %w", newHTTPError(response.StatusCode, body))
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&result)
	closeErr := response.Body.Close()
	if decodeErr != nil {
		return "", fmt.Errorf("decode Jamf token: %w", decodeErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close Jamf token response: %w", closeErr)
	}
	if result.AccessToken == "" || result.ExpiresIn <= 0 {
		return "", fmt.Errorf("decode Jamf token: access_token and positive expires_in are required")
	}
	c.accessToken = result.AccessToken
	c.tokenExpiresAt = c.now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return c.accessToken, nil
}

func (c *Client) invalidateToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken == token {
		c.accessToken = ""
		c.tokenExpiresAt = time.Time{}
	}
}

func encodePayload(payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Jamf request: %w", err)
	}
	return encoded, nil
}

func containsStatus(statuses []int, status int) bool {
	return slices.Contains(statuses, status)
}
