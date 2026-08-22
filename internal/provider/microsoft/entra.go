package microsoft

import (
	"context"
	"fmt"
	"sort"
	"strings"

	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

const graphPageSize int32 = 999

const checkMemberGroupsLimit = 20

// EnrichmentWarning reports a user whose optional group snapshot was incomplete.
type EnrichmentWarning struct {
	UserPrincipalName string
	Err               error
}

// Error returns a stable user-scoped warning.
func (w EnrichmentWarning) Error() string {
	return fmt.Sprintf("Entra group enrichment for %s: %v", w.UserPrincipalName, w.Err)
}

type enrichedUser struct {
	index  int
	groups []string
	err    error
}

// ListEntraUsers returns a complete directory snapshot for configured internal domains.
// Per-user group failures are returned as warnings and leave GroupsComplete false.
func (c *Client) ListEntraUsers(
	ctx context.Context,
	domains []string,
	groupAliases map[string][]string,
	concurrency int,
) ([]domain.User, []EnrichmentWarning, error) {
	if c == nil || c.graph == nil {
		return nil, nil, fmt.Errorf("graph client is required")
	}
	if concurrency <= 0 {
		return nil, nil, fmt.Errorf("concurrency must be greater than zero")
	}
	page, err := c.graph.Users().Get(ctx, &graphusers.UsersRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.UsersRequestBuilderGetQueryParameters{
			Select: []string{
				"id",
				"givenName",
				"surname",
				"userPrincipalName",
				"mailNickname",
				"department",
				"createdDateTime",
			},
			Top: new(graphPageSize),
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list Entra users: %w", err)
	}
	if page == nil {
		return nil, nil, fmt.Errorf("list Entra users: Graph returned no response")
	}

	domainSet := make(map[string]struct{}, len(domains))
	for _, value := range domains {
		domainSet[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	users := make([]domain.User, 0)
	seenLinks := make(map[string]struct{})
	for {
		pageUsers := page.GetValue()
		if pageUsers == nil {
			return nil, nil, fmt.Errorf("list Entra users: Graph response is missing value")
		}
		for _, user := range pageUsers {
			email := normalizeEmail(dereference(user.GetUserPrincipalName()))
			givenName := strings.TrimSpace(dereference(user.GetGivenName()))
			if email == "" || strings.Contains(email, "#ext#") || givenName == "" || !emailInDomains(email, domainSet) {
				continue
			}
			converted := domain.User{
				Present:           true,
				ID:                dereference(user.GetId()),
				GivenName:         givenName,
				Surname:           strings.TrimSpace(dereference(user.GetSurname())),
				MailNickname:      strings.ToLower(strings.TrimSpace(dereference(user.GetMailNickname()))),
				UserPrincipalName: email,
				Department:        strings.TrimSpace(dereference(user.GetDepartment())),
			}
			if createdAt := user.GetCreatedDateTime(); createdAt != nil {
				converted.CreatedAt = createdAt.UTC()
			}
			users = append(users, converted)
		}
		nextLink := page.GetOdataNextLink()
		if nextLink == nil || *nextLink == "" {
			break
		}
		if _, duplicate := seenLinks[*nextLink]; duplicate {
			return nil, nil, fmt.Errorf("page Entra users: Graph repeated next link")
		}
		seenLinks[*nextLink] = struct{}{}
		page, err = graphusers.NewUsersRequestBuilder(*nextLink, c.graph.GetAdapter()).Get(ctx, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("page Entra users: %w", err)
		}
		if page == nil {
			return nil, nil, fmt.Errorf("page Entra users: Graph returned no response")
		}
	}

	groupIDs, aliasesByID := prepareGroupAliases(groupAliases)
	if len(groupIDs) == 0 {
		for index := range users {
			users[index].GroupsComplete = true
		}
		sortUsers(users)
		return users, nil, nil
	}
	jobs := make(chan int, len(users))
	results := make(chan enrichedUser, len(users))
	for index := range users {
		jobs <- index
	}
	close(jobs)
	for range min(concurrency, len(users)) {
		go func() {
			for index := range jobs {
				groups, groupErr := c.checkGroupAliases(ctx, users[index].ID, groupIDs, aliasesByID)
				results <- enrichedUser{index: index, groups: groups, err: groupErr}
			}
		}()
	}
	warnings := make([]EnrichmentWarning, 0)
	for range users {
		result := <-results
		if result.err != nil {
			warnings = append(warnings, EnrichmentWarning{
				UserPrincipalName: users[result.index].UserPrincipalName,
				Err:               result.err,
			})
			continue
		}
		users[result.index].Groups = result.groups
		users[result.index].GroupsComplete = true
	}
	sortUsers(users)
	sort.Slice(warnings, func(left, right int) bool {
		return warnings[left].UserPrincipalName < warnings[right].UserPrincipalName
	})
	return users, warnings, nil
}

func (c *Client) checkGroupAliases(
	ctx context.Context,
	userID string,
	groupIDs []string,
	aliasesByID map[string][]string,
) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("entra user has no ID")
	}
	aliases := make(map[string]struct{})
	for start := 0; start < len(groupIDs); start += checkMemberGroupsLimit {
		end := min(start+checkMemberGroupsLimit, len(groupIDs))
		body := graphusers.NewItemCheckMemberGroupsPostRequestBody()
		body.SetGroupIds(groupIDs[start:end])
		response, err := c.graph.Users().ByUserId(userID).CheckMemberGroups().PostAsCheckMemberGroupsPostResponse(ctx, body, nil)
		if err != nil {
			return nil, fmt.Errorf("check Entra groups: %w", err)
		}
		if response == nil {
			return nil, fmt.Errorf("check Entra groups: Graph returned no response")
		}
		for _, groupID := range response.GetValue() {
			for _, alias := range aliasesByID[strings.ToLower(groupID)] {
				aliases[alias] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(aliases))
	for alias := range aliases {
		result = append(result, alias)
	}
	sort.Strings(result)
	return result, nil
}

func prepareGroupAliases(groupAliases map[string][]string) ([]string, map[string][]string) {
	aliasesByID := make(map[string][]string)
	for alias, groupIDs := range groupAliases {
		for _, groupID := range groupIDs {
			key := strings.ToLower(strings.TrimSpace(groupID))
			aliasesByID[key] = append(aliasesByID[key], alias)
		}
	}
	groupIDs := make([]string, 0, len(aliasesByID))
	for groupID := range aliasesByID {
		sort.Strings(aliasesByID[groupID])
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)
	return groupIDs, aliasesByID
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func emailInDomains(email string, domains map[string]struct{}) bool {
	separator := strings.LastIndexByte(email, '@')
	if separator < 0 {
		return false
	}
	_, ok := domains[email[separator+1:]]
	return ok
}

func sortUsers(users []domain.User) {
	sort.Slice(users, func(left, right int) bool {
		return users[left].UserPrincipalName < users[right].UserPrincipalName
	})
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
