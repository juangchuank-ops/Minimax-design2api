package main

import (
	"encoding/json"
	"testing"
)

// The console's 模型 page and the public /v1/{video,image}/models endpoints
// share these projections. A silent divergence between them would show the
// operator one thing and the API another, so the contract is asserted here.

func videoEntry(id, params string) videoModelEntry {
	m := videoModelEntry{ID: id, Name: id, Backend: id}
	if params != "" {
		if err := json.Unmarshal([]byte(params), &m.Params); err != nil {
			panic(err)
		}
	}
	return m
}

func TestVideoModelViewsSurfacesRouting(t *testing.T) {
	views := videoModelViews([]videoModelEntry{
		videoEntry("MiniMax-H3", `{"resolution":{"default":"768P","options":["768P","2K"]},"duration":{"options":["4","6","15"]}}`),
		videoEntry("veo-3.1-generate-001", ""),
		videoEntry("not-in-the-routing-table", ""),
	})
	if len(views) != 3 {
		t.Fatalf("got %d views, want 3", len(views))
	}

	for _, key := range []string{"id", "object", "owned_by", "name", "hub_model", "resolution", "ratio", "duration", "backend", "members_only"} {
		if _, ok := views[0][key]; !ok {
			t.Errorf("view is missing %q", key)
		}
	}

	if views[0]["backend"] != "minimax_v3" {
		t.Errorf("MiniMax-H3 backend = %v, want minimax_v3", views[0]["backend"])
	}
	if views[0]["members_only"] != false {
		t.Errorf("MiniMax-H3 members_only = %v, want false (non-members can use the H3 line)", views[0]["members_only"])
	}
	if views[1]["members_only"] != true {
		t.Errorf("veo-3.1 members_only = %v, want true", views[1]["members_only"])
	}

	// A catalogue model with no routing entry must still be listed, with an
	// empty backend rather than being dropped or panicking.
	if views[2]["backend"] != "" || views[2]["members_only"] != false {
		t.Errorf("unrouted model = %v / %v, want empty backend and false", views[2]["backend"], views[2]["members_only"])
	}

	// Durations are projected as the hub's option list, not pre-formatted.
	durations, ok := views[0]["duration"].([]string)
	if !ok || len(durations) != 3 {
		t.Fatalf("duration = %#v, want 3 options", views[0]["duration"])
	}
}

// members_only must agree with the backend table it is derived from — this is
// what the console's 会员专属 badge and the playground's warning both trust.
func TestVideoModelViewsMembersOnlyMatchesBackendTable(t *testing.T) {
	var entries []videoModelEntry
	for id := range videoModelTable {
		entries = append(entries, videoEntry(id, ""))
	}
	byID := map[string]map[string]any{}
	for _, v := range videoModelViews(entries) {
		byID[v["id"].(string)] = v
	}

	for id, target := range videoModelTable {
		want := videoBackends[target.Backend].MembersOnly
		got := byID[id]["members_only"]
		if got != want {
			t.Errorf("%s: members_only = %v, want %v (backend %s)", id, got, want, target.Backend)
		}
		if byID[id]["backend"] != target.Backend {
			t.Errorf("%s: backend = %v, want %v", id, byID[id]["backend"], target.Backend)
		}
	}
}

func TestImageModelViewsShape(t *testing.T) {
	entry := imageModelEntry{ID: "nano_banana_2_flash", Name: "Nano Banana 2 Flash", Backend: "nano_banana", ModelName: "nano_banana_2_flash"}
	if err := json.Unmarshal([]byte(`{"aspect_ratio":{"options":["1:1","16:9"]}}`), &entry.Params); err != nil {
		t.Fatalf("params: %v", err)
	}

	views := imageModelViews([]imageModelEntry{entry})
	if len(views) != 1 {
		t.Fatalf("got %d views, want 1", len(views))
	}
	for _, key := range []string{"id", "object", "owned_by", "name", "backend_model", "aspect_ratio", "resolution", "quality", "size", "supported_size"} {
		if _, ok := views[0][key]; !ok {
			t.Errorf("view is missing %q", key)
		}
	}
	if views[0]["owned_by"] != "nano_banana" {
		t.Errorf("owned_by = %v, want nano_banana", views[0]["owned_by"])
	}
	ratios, ok := views[0]["aspect_ratio"].([]string)
	if !ok || len(ratios) != 2 {
		t.Fatalf("aspect_ratio = %#v, want 2 options", views[0]["aspect_ratio"])
	}
	// An absent param must project as an empty list, never nil — the console
	// joins it directly and a null would render as "null" in the cell.
	if views[0]["quality"] == nil {
		t.Error("quality is nil; absent options must be an empty slice")
	}
}

// Both projections must be JSON-serialisable as-is: the admin mirror hands the
// maps straight to json.Encoder.
func TestCatalogViewsAreSerialisable(t *testing.T) {
	if _, err := json.Marshal(videoModelViews([]videoModelEntry{videoEntry("MiniMax-H3", "")})); err != nil {
		t.Fatalf("video views: %v", err)
	}
	if _, err := json.Marshal(imageModelViews([]imageModelEntry{{ID: "gpt-image-2"}})); err != nil {
		t.Fatalf("image views: %v", err)
	}
}
