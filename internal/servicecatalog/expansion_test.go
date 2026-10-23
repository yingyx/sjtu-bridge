package servicecatalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestMergeReviewedRecordsPreservesDeliveryIndices(t *testing.T) {
	base := []record{{
		entry: contracts.ServiceEntry{ID: "example", Destinations: []contracts.ServiceDestination{
			{Title: "Original portal", URL: "https://example.org/", Kind: "portal"},
			{Title: "Original guide", URL: "https://example.org/help", Kind: "official_documentation"},
		}},
		portals:    []portalBinding{{"original-portal", 0}, {"original-guide", 1}},
		navigation: []navigationBinding{{"example.navigate", "original-guide", 1}},
	}}
	updates := []record{{
		entry: contracts.ServiceEntry{ID: "example", Title: "Reviewed task", Destinations: []contracts.ServiceDestination{
			{Title: "New client guide", URL: "https://example.org/client", Kind: "official_documentation"},
			{Title: "Reviewed guide", URL: "https://example.org/help", Kind: "official_documentation"},
		}},
		portals:    []portalBinding{{"new-client", 0}, {"original-guide", 1}},
		navigation: []navigationBinding{{"example.navigate", "new-client", 0}},
	}, {entry: contracts.ServiceEntry{ID: "new-task", Title: "New task"}}}

	merged := mergeReviewedRecords(base, updates)
	if len(merged) != 2 || merged[1].entry.ID != "new-task" {
		t.Fatalf("new task not appended: %#v", merged)
	}
	r := merged[0]
	if r.entry.Title != "Reviewed task" || len(r.entry.Destinations) != 3 {
		t.Fatalf("reviewed metadata or destinations lost: %#v", r)
	}
	for index, want := range []string{"https://example.org/", "https://example.org/help", "https://example.org/client"} {
		if r.entry.Destinations[index].URL != want {
			t.Fatalf("destination %d moved: %#v", index, r.entry.Destinations)
		}
	}
	if r.entry.Destinations[1].Title != "Reviewed guide" {
		t.Fatal("same-URL destination was not enriched")
	}
	if !reflect.DeepEqual(r.portals, []portalBinding{{"original-portal", 0}, {"original-guide", 1}, {"new-client", 2}}) {
		t.Fatalf("legacy portal indices changed or new binding not remapped: %#v", r.portals)
	}
	if !reflect.DeepEqual(r.navigation, []navigationBinding{{"example.navigate", "original-guide", 1}, {"example.navigate", "new-client", 2}}) {
		t.Fatalf("navigation indices changed or new binding not remapped: %#v", r.navigation)
	}
}

func TestExpansionPreservesExistingPortalProjection(t *testing.T) {
	portals := PortalItems()
	for sourceID, want := range map[string]struct{ task, url string }{
		"notes": {"notes", "https://notes.sjtu.edu.cn/"},
		"latex": {"latex", "https://latex.sjtu.edu.cn/"},
		"git":   {"gitlab", "https://git.sjtu.edu.cn/"},
		"chat":  {"campus-ai", "https://chat.sjtu.edu.cn/"},
	} {
		matches := 0
		for _, item := range portals {
			if item.SourceID != sourceID {
				continue
			}
			matches++
			if item.URL != want.url || field(item.Fields, "service_task_id") != want.task {
				t.Fatalf("legacy portal %s changed target: %#v", sourceID, item)
			}
			e, err := Describe(nil, want.task)
			if err != nil || !slices.ContainsFunc(e.Destinations, func(d contracts.ServiceDestination) bool { return d.URL == item.URL && d.Title == item.Title }) {
				t.Fatalf("portal %s is no longer a canonical task destination: %#v, %v", sourceID, e, err)
			}
		}
		if matches != 1 {
			t.Fatalf("legacy portal %s projected %d times", sourceID, matches)
		}
	}
}

func TestExternalClientMetadataIsIndependentAcrossReads(t *testing.T) {
	for _, id := range []string{"notes", "gitlab", "campus-ai"} {
		t.Run(id, func(t *testing.T) {
			before, err := Describe(nil, id)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := json.Marshal(before)
			if err != nil || len(before.ExternalClients) == 0 {
				t.Fatalf("missing external guidance: %#v, %v", before, err)
			}
			mutate := func(e *contracts.ServiceEntry) {
				t.Helper()
				for i := range e.ExternalClients {
					tool := &e.ExternalClients[i]
					if len(tool.Setup) == 0 || len(tool.Limitations) == 0 {
						t.Fatalf("guide lacks nested metadata: %#v", tool)
					}
					tool.Title = "MUTATED"
					tool.Setup[0] = "MUTATED"
					tool.Limitations[0] = "MUTATED"
				}
			}
			mutate(&before)
			page, err := Search(nil, contracts.ServiceFilter{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(page.Items, func(e contracts.ServiceEntry) bool { return e.ID == id })
			if index < 0 {
				t.Fatalf("task %s missing from search", id)
			}
			searchRaw, _ := json.Marshal(page.Items[index])
			if string(searchRaw) != string(baseline) {
				t.Fatal("Describe mutation leaked into Search")
			}
			mutate(&page.Items[index])
			after, err := Describe(nil, id)
			if err != nil {
				t.Fatal(err)
			}
			afterRaw, _ := json.Marshal(after)
			if string(afterRaw) != string(baseline) {
				t.Fatal("Search mutation leaked into Describe")
			}
		})
	}
}
