package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/provider/microsoft"
)

type fakeEntraClient struct {
	delta  func(string) (microsoft.EntraUserDelta, error)
	group  func(string) ([]string, error)
	links  []string
	groups []string
}

func (f *fakeEntraClient) ListEntraUserDelta(_ context.Context, link string) (microsoft.EntraUserDelta, error) {
	f.links = append(f.links, link)
	return f.delta(link)
}

func (f *fakeEntraClient) ListEntraGroupUserIDs(_ context.Context, groupID string) ([]string, error) {
	f.groups = append(f.groups, groupID)
	return f.group(groupID)
}

func TestEntraSourceMaterializesUserDelta(t *testing.T) {
	groupRound := 0
	client := &fakeEntraClient{
		delta: func(link string) (microsoft.EntraUserDelta, error) {
			switch link {
			case "":
				return microsoft.EntraUserDelta{
					Changes: []microsoft.EntraUserChange{
						{User: testEntraUser("user-1", "casey@example.invalid")},
						{User: testEntraUser("user-2", "taylor@example.invalid")},
						{User: testEntraUser("external", "external@other.invalid")},
					},
					DeltaLink: "delta-1",
				}, nil
			case "delta-1":
				return microsoft.EntraUserDelta{
					Changes: []microsoft.EntraUserChange{
						{User: domain.User{ID: "user-2"}, Removed: true},
						{User: testEntraUser("user-3", "morgan@example.invalid")},
					},
					DeltaLink: "delta-2",
				}, nil
			default:
				t.Fatalf("unexpected delta link %q", link)
				return microsoft.EntraUserDelta{}, nil
			}
		},
		group: func(groupID string) ([]string, error) {
			if groupID != "group-1" {
				t.Fatalf("unexpected group ID %q", groupID)
			}
			groupRound++
			if groupRound == 1 {
				return []string{"user-1"}, nil
			}
			return []string{"user-1", "user-3"}, nil
		},
	}
	source := newEntraSource(client, []string{"example.invalid"}, map[string][]string{"staff": {"group-1"}})

	first, warnings, err := source.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want nil", warnings)
	}
	if got, want := userEmails(first), []string{"casey@example.invalid", "taylor@example.invalid"}; !slices.Equal(got, want) {
		t.Fatalf("first users = %v, want %v", got, want)
	}
	if got, want := first[0].Groups, []string{"staff"}; !slices.Equal(got, want) {
		t.Errorf("first groups = %v, want %v", got, want)
	}
	if !first[0].GroupsComplete || !first[1].GroupsComplete {
		t.Error("group snapshot is incomplete")
	}

	second, _, err := source.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := userEmails(second), []string{"casey@example.invalid", "morgan@example.invalid"}; !slices.Equal(got, want) {
		t.Fatalf("second users = %v, want %v", got, want)
	}
	if got, want := client.links, []string{"", "delta-1"}; !slices.Equal(got, want) {
		t.Errorf("delta links = %v, want %v", got, want)
	}
}

func TestEntraSourceOnlyCommitsCompleteRound(t *testing.T) {
	groupCalls := 0
	client := &fakeEntraClient{
		delta: func(link string) (microsoft.EntraUserDelta, error) {
			if link != "" {
				t.Fatalf("delta link = %q, want initial link", link)
			}
			return microsoft.EntraUserDelta{
				Changes:   []microsoft.EntraUserChange{{User: testEntraUser("user-1", "casey@example.invalid")}},
				DeltaLink: "delta-1",
			}, nil
		},
		group: func(string) ([]string, error) {
			groupCalls++
			if groupCalls == 1 {
				return nil, errors.New("group unavailable")
			}
			return []string{"user-1"}, nil
		},
	}
	source := newEntraSource(client, []string{"example.invalid"}, map[string][]string{"staff": {"group-1"}})

	if _, _, err := source.ListUsers(context.Background()); err == nil {
		t.Fatal("ListUsers succeeded with an incomplete group snapshot")
	}
	users, _, err := source.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(users), 1; got != want {
		t.Fatalf("users = %d, want %d", got, want)
	}
	if got, want := client.links, []string{"", ""}; !slices.Equal(got, want) {
		t.Errorf("delta links = %v, want %v", got, want)
	}
}

func TestEntraSourceRebuildsAfterExpiredDelta(t *testing.T) {
	client := &fakeEntraClient{
		delta: func(link string) (microsoft.EntraUserDelta, error) {
			if link == "expired" {
				return microsoft.EntraUserDelta{}, microsoft.ErrDeltaExpired
			}
			return microsoft.EntraUserDelta{
				Changes:   []microsoft.EntraUserChange{{User: testEntraUser("new", "new@example.invalid")}},
				DeltaLink: "fresh",
			}, nil
		},
		group: func(string) ([]string, error) { return nil, nil },
	}
	source := newEntraSource(client, []string{"example.invalid"}, nil)
	source.deltaLink = "expired"
	source.users["old"] = testEntraUser("old", "old@example.invalid")

	users, _, err := source.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := userEmails(users), []string{"new@example.invalid"}; !slices.Equal(got, want) {
		t.Errorf("users = %v, want %v", got, want)
	}
	if got, want := client.links, []string{"expired", ""}; !slices.Equal(got, want) {
		t.Errorf("delta links = %v, want %v", got, want)
	}
}

func testEntraUser(id, email string) domain.User {
	return domain.User{Present: true, ID: id, GivenName: "Example", UserPrincipalName: email}
}

func userEmails(users []domain.User) []string {
	emails := make([]string, len(users))
	for index, user := range users {
		emails[index] = user.UserPrincipalName
	}
	return emails
}
