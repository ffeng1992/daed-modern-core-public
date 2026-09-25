package group

import (
	"testing"

	"github.com/daeuniverse/dae-wing/db"
)

func TestSubscriptionBindingKeepsMatchingNodes(t *testing.T) {
	filter := "^JP-"
	binding := &db.GroupSubscription{
		NameFilterRegex: &filter,
		Subscription: db.Subscription{Node: []db.Node{
			{Name: "JP-fast"},
			{Name: "US-fast"},
			{Name: "JP-backup"},
		}},
	}
	resolver := &SubscriptionBindingResolver{GroupSubscription: binding}
	matched, err := resolver.MatchedNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 2 || matched[0].Node.Name != "JP-fast" || matched[1].Node.Name != "JP-backup" {
		t.Fatalf("positive matches were not retained: %+v", matched)
	}
	count, err := resolver.MatchedCount()
	if err != nil || count != 2 {
		t.Fatalf("matched count = %d, err = %v; want 2", count, err)
	}
}
