package riven

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

// gqlStub serves a fixed GraphQL response body for every /graphql POST and
// records the decoded request bodies it received.
type gqlStub struct {
	mu        sync.Mutex
	bodies    []gqlRequest
	responses []string // one response body per call, in order; last is reused once exhausted
}

func (s *gqlStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&body)

		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		idx := len(s.bodies) - 1
		s.mu.Unlock()

		if idx >= len(s.responses) {
			idx = len(s.responses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.responses[idx]))
	})
}

func newStub(t *testing.T, responses ...string) (*gqlStub, *httpclient.Client) {
	t.Helper()
	stub := &gqlStub{responses: responses}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	return stub, httpclient.New(srv.URL, "k", nil)
}

func TestRequestMovieSuccess(t *testing.T) {
	stub, client := newStub(t, `{"data":{"requestMovie":{"success":true,"message":"Movie request created successfully.","item":{"state":"REQUESTED"}}}}`)

	title := "Arrival"
	tmdb := "42"
	requestedBy := "silo"
	outcome, err := RequestMovie(context.Background(), client, title, nil, &tmdb, &requestedBy)
	if err != nil {
		t.Fatalf("RequestMovie: %v", err)
	}
	if !outcome.Success || outcome.Conflict || outcome.State != "REQUESTED" {
		t.Fatalf("bad outcome: %+v", outcome)
	}

	if len(stub.bodies) != 1 {
		t.Fatalf("want 1 request, got %d", len(stub.bodies))
	}
	vars, ok := stub.bodies[0].Variables.(map[string]any)
	if !ok {
		t.Fatalf("bad variables: %#v", stub.bodies[0].Variables)
	}
	input, ok := vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("bad input: %#v", vars)
	}
	if input["title"] != title || input["tmdbId"] != tmdb || input["requestedBy"] != requestedBy {
		t.Fatalf("bad input: %+v", input)
	}
	if _, present := input["imdbId"]; present {
		t.Fatalf("imdbId must be omitted when nil: %+v", input)
	}
}

func TestRequestMovieConflict(t *testing.T) {
	_, client := newStub(t, `{"data":{"requestMovie":{"success":false,"message":"A request for this movie already exists.","item":null}}}`)

	tmdb := "42"
	outcome, err := RequestMovie(context.Background(), client, "Arrival", nil, &tmdb, nil)
	if err != nil {
		t.Fatalf("RequestMovie: %v", err)
	}
	if !outcome.Conflict || outcome.Success {
		t.Fatalf("want conflict outcome, got %+v", outcome)
	}
}

func TestRequestShowIncludesSeasonsField(t *testing.T) {
	stub, client := newStub(t, `{"data":{"requestShow":{"success":true,"message":"Show request created successfully.","item":{"state":"REQUESTED"}}}}`)

	tvdb := "99"
	outcome, err := RequestShow(context.Background(), client, "Severance", nil, &tvdb, nil)
	if err != nil {
		t.Fatalf("RequestShow: %v", err)
	}
	if !outcome.Success {
		t.Fatalf("bad outcome: %+v", outcome)
	}
	if !strings.Contains(stub.bodies[0].Query, "requestShow") {
		t.Fatalf("expected requestShow mutation, got query: %s", stub.bodies[0].Query)
	}
}

func TestGraphQLErrorSurfaces(t *testing.T) {
	_, client := newStub(t, `{"errors":[{"message":"Forbidden"}]}`)

	tmdb := "1"
	_, err := RequestMovie(context.Background(), client, "X", nil, &tmdb, nil)
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("want Forbidden error, got %v", err)
	}
}

func TestMediaItemStateFound(t *testing.T) {
	_, client := newStub(t, `{"data":{"mediaItemByTmdb":{"state":"Completed"}}}`)

	state, err := MediaItemState(context.Background(), client, "tmdb", "42")
	if err != nil {
		t.Fatalf("MediaItemState: %v", err)
	}
	if state != "Completed" {
		t.Fatalf("want Completed, got %q", state)
	}
}

func TestMediaItemStateNotFound(t *testing.T) {
	_, client := newStub(t, `{"data":{"mediaItemByTvdb":null}}`)

	state, err := MediaItemState(context.Background(), client, "tvdb", "99")
	if err != nil {
		t.Fatalf("MediaItemState: %v", err)
	}
	if state != "" {
		t.Fatalf("want empty state, got %q", state)
	}
}

func TestPingSuccess(t *testing.T) {
	_, client := newStub(t, `{"data":{"__typename":"Query"}}`)
	if err := Ping(context.Background(), client); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPingUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := httpclient.New(srv.URL, "bad-key", nil)
	if err := Ping(context.Background(), client); err == nil {
		t.Fatal("want error for unauthorized ping")
	}
}
