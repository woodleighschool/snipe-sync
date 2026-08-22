package microsoft

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	adapter, err := msgraphsdk.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		&authentication.AnonymousAuthenticationProvider{},
		nil,
		nil,
		httpClient,
	)
	if err != nil {
		t.Fatalf("create Graph request adapter: %v", err)
	}
	adapter.SetBaseUrl("https://graph.test/v1.0")
	return newClient(msgraphsdk.NewGraphServiceClient(adapter))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, body any) *http.Response {
	var content bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&content).Encode(body); err != nil {
			panic(err)
		}
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(&content),
		ContentLength: int64(content.Len()),
	}
}
