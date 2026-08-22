// Package microsoft reads Entra and Intune inventory through Microsoft Graph.
package microsoft

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

const defaultGraphBaseURL = "https://graph.microsoft.com/v1.0"

var graphScopes = []string{"https://graph.microsoft.com/.default"}

// Config contains one Microsoft Graph application connection.
type Config struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	BaseURL      string
}

// Client shares one credential and Graph SDK client across Intune and Entra operations.
type Client struct {
	graph *msgraphsdk.GraphServiceClient
}

// NewClient creates an application-authenticated Microsoft Graph client.
func NewClient(config Config) (*Client, error) {
	if config.TenantID == "" || config.ClientID == "" || config.ClientSecret == "" {
		return nil, fmt.Errorf("tenant ID, client ID, and client secret are required")
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultGraphBaseURL
	}
	credential, err := azidentity.NewClientSecretCredential(
		config.TenantID,
		config.ClientID,
		config.ClientSecret,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Microsoft credential: %w", err)
	}
	graph, err := msgraphsdk.NewGraphServiceClientWithCredentials(credential, graphScopes)
	if err != nil {
		return nil, fmt.Errorf("create Microsoft Graph client: %w", err)
	}
	graph.GetAdapter().SetBaseUrl(baseURL)
	return newClient(graph), nil
}

func newClient(graph *msgraphsdk.GraphServiceClient) *Client {
	return &Client{graph: graph}
}
