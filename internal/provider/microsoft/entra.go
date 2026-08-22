package microsoft

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	kiota "github.com/microsoft/kiota-abstractions-go"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

// ErrDeltaExpired indicates that Graph can no longer continue from a stored delta link.
var ErrDeltaExpired = errors.New("entra user delta link expired")

const graphPageSize int32 = 999

// EntraUserChange is one created, updated, or removed directory user.
type EntraUserChange struct {
	User    domain.User
	Removed bool
}

// EntraUserDelta contains a complete delta round and the link for the next round.
type EntraUserDelta struct {
	Changes   []EntraUserChange
	DeltaLink string
}

// ListEntraUserDelta reads a complete initial or incremental user delta round.
func (c *Client) ListEntraUserDelta(ctx context.Context, deltaLink string) (EntraUserDelta, error) {
	if c == nil || c.graph == nil {
		return EntraUserDelta{}, fmt.Errorf("graph client is required")
	}

	var (
		page graphusers.DeltaGetResponseable
		err  error
	)
	if deltaLink == "" {
		page, err = c.graph.Users().Delta().GetAsDeltaGetResponse(ctx, &graphusers.DeltaRequestBuilderGetRequestConfiguration{
			QueryParameters: &graphusers.DeltaRequestBuilderGetQueryParameters{Select: []string{
				"id",
				"givenName",
				"surname",
				"userPrincipalName",
				"mailNickname",
				"department",
				"createdDateTime",
			}},
		})
	} else {
		page, err = graphusers.NewDeltaRequestBuilder(deltaLink, c.graph.GetAdapter()).GetAsDeltaGetResponse(ctx, nil)
	}
	if err != nil {
		return EntraUserDelta{}, classifyDeltaError(err)
	}
	if page == nil {
		return EntraUserDelta{}, fmt.Errorf("list Entra user delta: Graph returned no response")
	}

	result := EntraUserDelta{}
	seenLinks := make(map[string]struct{})
	for {
		pageUsers := page.GetValue()
		if pageUsers == nil {
			return EntraUserDelta{}, fmt.Errorf("list Entra user delta: Graph response is missing value")
		}
		for _, user := range pageUsers {
			if user == nil {
				return EntraUserDelta{}, fmt.Errorf("list Entra user delta: Graph returned a null user")
			}
			id := strings.TrimSpace(dereference(user.GetId()))
			if id == "" {
				return EntraUserDelta{}, fmt.Errorf("list Entra user delta: Graph returned a user without an ID")
			}
			change := EntraUserChange{User: domain.User{
				Present:           true,
				ID:                id,
				GivenName:         strings.TrimSpace(dereference(user.GetGivenName())),
				Surname:           strings.TrimSpace(dereference(user.GetSurname())),
				MailNickname:      strings.ToLower(strings.TrimSpace(dereference(user.GetMailNickname()))),
				UserPrincipalName: normalizeEmail(dereference(user.GetUserPrincipalName())),
				Department:        strings.TrimSpace(dereference(user.GetDepartment())),
			}}
			if createdAt := user.GetCreatedDateTime(); createdAt != nil {
				change.User.CreatedAt = createdAt.UTC()
			}
			_, change.Removed = user.GetAdditionalData()["@removed"]
			result.Changes = append(result.Changes, change)
		}

		nextLink := page.GetOdataNextLink()
		if nextLink == nil || *nextLink == "" {
			break
		}
		if _, duplicate := seenLinks[*nextLink]; duplicate {
			return EntraUserDelta{}, fmt.Errorf("page Entra user delta: Graph repeated next link")
		}
		seenLinks[*nextLink] = struct{}{}
		page, err = graphusers.NewDeltaRequestBuilder(*nextLink, c.graph.GetAdapter()).GetAsDeltaGetResponse(ctx, nil)
		if err != nil {
			return EntraUserDelta{}, classifyDeltaError(err)
		}
		if page == nil {
			return EntraUserDelta{}, fmt.Errorf("page Entra user delta: Graph returned no response")
		}
	}

	if link := page.GetOdataDeltaLink(); link != nil {
		result.DeltaLink = strings.TrimSpace(*link)
	}
	if result.DeltaLink == "" {
		return EntraUserDelta{}, fmt.Errorf("list Entra user delta: Graph response is missing delta link")
	}
	return result, nil
}

// ListEntraGroupUserIDs returns the IDs of every transitive user member of a group.
func (c *Client) ListEntraGroupUserIDs(ctx context.Context, groupID string) ([]string, error) {
	if c == nil || c.graph == nil {
		return nil, fmt.Errorf("graph client is required")
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("group ID is required")
	}

	headers := kiota.NewRequestHeaders()
	headers.TryAdd("ConsistencyLevel", "eventual")
	page, err := c.graph.Groups().ByGroupId(groupID).TransitiveMembers().GraphUser().Get(ctx,
		&graphgroups.ItemTransitiveMembersGraphUserRequestBuilderGetRequestConfiguration{
			Headers: headers,
			QueryParameters: &graphgroups.ItemTransitiveMembersGraphUserRequestBuilderGetQueryParameters{
				Count:  new(true),
				Select: []string{"id"},
				Top:    new(graphPageSize),
			},
		})
	if err != nil {
		return nil, fmt.Errorf("list transitive users for Entra group %s: %w", groupID, err)
	}
	if page == nil {
		return nil, fmt.Errorf("list transitive users for Entra group %s: Graph returned no response", groupID)
	}

	memberIDs := make(map[string]struct{})
	seenLinks := make(map[string]struct{})
	for {
		members := page.GetValue()
		if members == nil {
			return nil, fmt.Errorf("list transitive users for Entra group %s: Graph response is missing value", groupID)
		}
		for _, member := range members {
			if member == nil {
				return nil, fmt.Errorf("list transitive users for Entra group %s: Graph returned a null user", groupID)
			}
			id := strings.TrimSpace(dereference(member.GetId()))
			if id == "" {
				return nil, fmt.Errorf("list transitive users for Entra group %s: Graph returned a user without an ID", groupID)
			}
			memberIDs[id] = struct{}{}
		}

		nextLink := page.GetOdataNextLink()
		if nextLink == nil || *nextLink == "" {
			break
		}
		if _, duplicate := seenLinks[*nextLink]; duplicate {
			return nil, fmt.Errorf("page transitive users for Entra group %s: Graph repeated next link", groupID)
		}
		seenLinks[*nextLink] = struct{}{}
		page, err = graphgroups.NewItemTransitiveMembersGraphUserRequestBuilder(*nextLink, c.graph.GetAdapter()).Get(ctx,
			&graphgroups.ItemTransitiveMembersGraphUserRequestBuilderGetRequestConfiguration{Headers: headers})
		if err != nil {
			return nil, fmt.Errorf("page transitive users for Entra group %s: %w", groupID, err)
		}
		if page == nil {
			return nil, fmt.Errorf("page transitive users for Entra group %s: Graph returned no response", groupID)
		}
	}

	result := make([]string, 0, len(memberIDs))
	for id := range memberIDs {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func classifyDeltaError(err error) error {
	var apiError interface{ GetStatusCode() int }
	if errors.As(err, &apiError) && apiError.GetStatusCode() == http.StatusGone {
		return fmt.Errorf("%w: %w", ErrDeltaExpired, err)
	}
	return fmt.Errorf("list Entra user delta: %w", err)
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
