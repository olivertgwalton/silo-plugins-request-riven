package scansource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// gqlHandler serves canned itemsByState/filesystemEntries responses keyed by
// itemType/mediaItemId so a test can script a whole PollChanges call without
// caring about internal call order.
type gqlHandler struct {
	completedByType map[string][]int64 // itemType -> completed ids
	pathsByID       map[int64][]string // mediaItemId -> filesystem paths
	calls           []map[string]any   // decoded variables, one per request
}

func newGQLServer(t *testing.T, h *gqlHandler) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.calls = append(h.calls, body.Variables)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "itemsByState"):
			itemType, _ := body.Variables["itemType"].(string)
			ids := h.completedByType[itemType]
			items := make([]map[string]any, len(ids))
			for i, id := range ids {
				items[i] = map[string]any{"id": id}
			}
			resp := map[string]any{"data": map[string]any{"itemsByState": items}}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.Contains(body.Query, "filesystemEntries"):
			idFloat, _ := body.Variables["id"].(float64)
			paths := h.pathsByID[int64(idFloat)]
			entries := make([]map[string]any, len(paths))
			for i, p := range paths {
				entries[i] = map[string]any{"path": p}
			}
			resp := map[string]any{"data": map[string]any{"filesystemEntries": entries}}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "unexpected query: "+body.Query, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestPollChangesFirstRunSeedsMarkerWithoutChanges(t *testing.T) {
	h := &gqlHandler{
		completedByType: map[string][]int64{
			"MOVIE":   {1, 2},
			"EPISODE": {10},
		},
		pathsByID: map[int64][]string{
			1:  {"/mount/movies/A/a.mkv"},
			2:  {"/mount/movies/B/b.mkv"},
			10: {"/mount/shows/C/S01E01.mkv"},
		},
	}
	url := newGQLServer(t, h)

	srv := New()
	resp, err := srv.PollChanges(context.Background(), &pluginv1.PollChangesRequest{
		Marker:     "",
		Connection: &pluginv1.ResolvedConnection{BaseUrl: url, ApiKey: "k"},
	})
	if err != nil {
		t.Fatalf("PollChanges: %v", err)
	}
	if len(resp.GetChanges()) != 0 {
		t.Fatalf("first run: want no changes, got %+v", resp.GetChanges())
	}
	if resp.GetNextMarker() == "" {
		t.Fatal("first run: want a seeded marker, got empty")
	}

	// Filesystem entries must never be queried on the seeding run: nothing
	// new was reported, so there is nothing to resolve a path for.
	for _, call := range h.calls {
		if _, ok := call["id"]; ok {
			t.Fatalf("first run must not query filesystemEntries, got call: %+v", call)
		}
	}
}

func TestPollChangesReportsNewlyCompletedItems(t *testing.T) {
	h := &gqlHandler{
		completedByType: map[string][]int64{
			"MOVIE":   {1, 2},
			"EPISODE": {},
		},
		pathsByID: map[int64][]string{
			2: {"/mount/movies/B/b.mkv"},
		},
	}
	url := newGQLServer(t, h)

	seeded, err := encodeMarker(map[int64]struct{}{1: {}})
	if err != nil {
		t.Fatalf("encodeMarker: %v", err)
	}

	srv := New()
	resp, err := srv.PollChanges(context.Background(), &pluginv1.PollChangesRequest{
		Marker:     seeded,
		Connection: &pluginv1.ResolvedConnection{BaseUrl: url, ApiKey: "k"},
	})
	if err != nil {
		t.Fatalf("PollChanges: %v", err)
	}
	changes := resp.GetChanges()
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %+v", changes)
	}
	if changes[0].GetSourcePath() != "/mount/movies/B/b.mkv" {
		t.Fatalf("bad source path: %+v", changes[0])
	}
	if changes[0].GetScope() != pluginv1.ScanSourceChangeScope_SCAN_SOURCE_CHANGE_SCOPE_FILE {
		t.Fatalf("bad scope: %v", changes[0].GetScope())
	}

	next, err := decodeMarker(resp.GetNextMarker())
	if err != nil {
		t.Fatalf("decodeMarker: %v", err)
	}
	if _, ok := next[1]; !ok {
		t.Fatal("next marker must retain previously seen id 1")
	}
	if _, ok := next[2]; !ok {
		t.Fatal("next marker must add newly completed id 2")
	}
}

func TestPollChangesNoNewItemsReturnsSameMarkerContent(t *testing.T) {
	h := &gqlHandler{
		completedByType: map[string][]int64{
			"MOVIE":   {1},
			"EPISODE": {},
		},
	}
	url := newGQLServer(t, h)

	seeded, err := encodeMarker(map[int64]struct{}{1: {}})
	if err != nil {
		t.Fatalf("encodeMarker: %v", err)
	}

	srv := New()
	resp, err := srv.PollChanges(context.Background(), &pluginv1.PollChangesRequest{
		Marker:     seeded,
		Connection: &pluginv1.ResolvedConnection{BaseUrl: url, ApiKey: "k"},
	})
	if err != nil {
		t.Fatalf("PollChanges: %v", err)
	}
	if len(resp.GetChanges()) != 0 {
		t.Fatalf("want no changes, got %+v", resp.GetChanges())
	}
	if resp.GetNextMarker() != seeded {
		t.Fatalf("marker content should be unchanged: got %q want %q", resp.GetNextMarker(), seeded)
	}
}

func TestPollChangesPropagatesGraphQLErrors(t *testing.T) {
	srv0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
	}))
	t.Cleanup(srv0.Close)

	srv := New()
	_, err := srv.PollChanges(context.Background(), &pluginv1.PollChangesRequest{
		Marker:     "",
		Connection: &pluginv1.ResolvedConnection{BaseUrl: srv0.URL, ApiKey: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want boom error, got %v", err)
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	original := map[int64]struct{}{3: {}, 1: {}, 2: {}}
	encoded, err := encodeMarker(original)
	if err != nil {
		t.Fatalf("encodeMarker: %v", err)
	}
	decoded, err := decodeMarker(encoded)
	if err != nil {
		t.Fatalf("decodeMarker: %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("bad round trip: %+v", decoded)
	}
	for id := range original {
		if _, ok := decoded[id]; !ok {
			t.Fatalf("missing id %d after round trip", id)
		}
	}
}

func TestDecodeMarkerEmpty(t *testing.T) {
	decoded, err := decodeMarker("")
	if err != nil {
		t.Fatalf("decodeMarker: %v", err)
	}
	if len(decoded) != 0 {
		t.Fatalf("want empty marker, got %+v", decoded)
	}
}

func TestDecodeMarkerInvalidJSON(t *testing.T) {
	if _, err := decodeMarker("not json"); err == nil {
		t.Fatal("want error for invalid marker JSON")
	}
}
