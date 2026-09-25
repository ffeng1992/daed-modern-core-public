package config

import (
	"testing"

	"github.com/daeuniverse/dae-wing/db"
)

func TestConfigGenerationKeepsMatchingSubscriptionNodes(t *testing.T) {
	filter := "^JP-"
	binding := &db.GroupSubscription{
		NameFilterRegex: &filter,
		Subscription: db.Subscription{Node: []db.Node{
			{Name: "JP-fast"},
			{Name: "US-fast"},
			{Name: "JP-backup"},
		}},
	}
	matched, err := matchedNodesForGroupSubscription(binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 2 || matched[0].Name != "JP-fast" || matched[1].Name != "JP-backup" {
		t.Fatalf("config generation discarded matching nodes: %+v", matched)
	}
}
