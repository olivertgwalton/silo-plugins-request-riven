package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// fakeRiven serves canned GraphQL responses keyed by a substring match
// against the incoming query, so one stub can stand in for a movie request,
// a show request, and a media-item status lookup.
type fakeRiven struct {
	byQuerySubstring map[string]string
	calls            int
}

func (f *fakeRiven) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for substr, resp := range f.byQuerySubstring {
			if strings.Contains(body.Query, substr) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		http.Error(w, "no stub matched query: "+body.Query, http.StatusNotFound)
	})
}

func startFakeRiven(t *testing.T, byQuerySubstring map[string]string) (*httptest.Server, *fakeRiven) {
	t.Helper()
	f := &fakeRiven{byQuerySubstring: byQuerySubstring}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return srv, f
}

func routerConn(id, baseURL string) *pluginv1.RouterConnection {
	return &pluginv1.RouterConnection{Id: id, BaseUrl: baseURL, ApiKey: "k"}
}

func TestFulfillMovie(t *testing.T) {
	srv, _ := startFakeRiven(t, map[string]string{
		"requestMovie": `{"data":{"requestMovie":{"success":true,"message":"Movie request created successfully.","item":{"state":"REQUESTED"}}}}`,
	})

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "42"}},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 1 {
		t.Fatalf("want 1 target, got %d msg=%q", len(resp.GetTargets()), resp.GetMessage())
	}
	tgt := resp.GetTargets()[0]
	if tgt.GetStatus() != "queued" || tgt.GetQuality() != "1080p" || tgt.GetExternalId() != "42" || tgt.GetExternalStatus() != "REQUESTED" {
		t.Fatalf("bad target: %+v", tgt)
	}
}

func TestFulfillSeriesUsesTVDB(t *testing.T) {
	srv, _ := startFakeRiven(t, map[string]string{
		"requestShow": `{"data":{"requestShow":{"success":true,"message":"Show request created successfully.","item":{"state":"REQUESTED"}}}}`,
	})

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "series", Title: "Severance", ExternalIds: map[string]string{"tvdb": "99", "tmdb": "should-be-ignored"}},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "2160p", Is4K: true}},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 1 {
		t.Fatalf("want 1 target, got %d msg=%q", len(resp.GetTargets()), resp.GetMessage())
	}
	tgt := resp.GetTargets()[0]
	if tgt.GetExternalId() != "99" || tgt.GetQuality() != "2160p" {
		t.Fatalf("bad target: %+v", tgt)
	}
}

func TestFulfillNoUsableIDShortCircuits(t *testing.T) {
	// No server is started: a call would fail the test via connection refused,
	// proving Fulfill returns before dialing out when it has no id to send.
	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", Title: "No IDs"},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", "http://127.0.0.1:1")},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 0 || resp.GetMessage() == "" {
		t.Fatalf("want no targets and a message, got %+v", resp)
	}
}

func TestFulfillConflictIsQueuedNotFailed(t *testing.T) {
	srv, _ := startFakeRiven(t, map[string]string{
		"requestMovie": `{"data":{"requestMovie":{"success":false,"message":"A request for this movie already exists.","item":null}}}`,
	})

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "42"}},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	tgt := resp.GetTargets()[0]
	if tgt.GetStatus() != "queued" {
		t.Fatalf("want conflict mapped to queued, got %+v", tgt)
	}
}

func TestFulfillPartialContainmentAcrossConnections(t *testing.T) {
	okSrv, _ := startFakeRiven(t, map[string]string{
		"requestMovie": `{"data":{"requestMovie":{"success":true,"message":"ok","item":{"state":"REQUESTED"}}}}`,
	})
	errSrv, _ := startFakeRiven(t, map[string]string{
		"requestMovie": `{"errors":[{"message":"Forbidden"}]}`,
	})

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request: &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "42"}},
		Connections: []*pluginv1.RouterConnection{
			routerConn("good", okSrv.URL),
			routerConn("bad", errSrv.URL),
		},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 2 {
		t.Fatalf("want 2 targets, got %d", len(resp.GetTargets()))
	}
	byConn := map[string]*pluginv1.FulfillmentTarget{}
	for _, tgt := range resp.GetTargets() {
		byConn[tgt.GetConnectionId()] = tgt
	}
	if byConn["good"].GetStatus() != "queued" {
		t.Fatalf("good connection should be queued: %+v", byConn["good"])
	}
	if byConn["bad"].GetStatus() != "failed" || !strings.Contains(byConn["bad"].GetMessage(), "Forbidden") {
		t.Fatalf("bad connection should be failed with message: %+v", byConn["bad"])
	}
}

func TestCheckStatusMapsState(t *testing.T) {
	srv, _ := startFakeRiven(t, map[string]string{
		"mediaItemByTmdb": `{"data":{"mediaItemByTmdb":{"state":"Scraped"}}}`,
	})

	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "42"}},
		Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "c1", ExternalId: "42"}},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if len(resp.GetStatuses()) != 1 {
		t.Fatalf("want 1 status, got %d", len(resp.GetStatuses()))
	}
	st := resp.GetStatuses()[0]
	if st.GetStatus() != "downloading" || st.GetExternalStatus() != "Scraped" {
		t.Fatalf("bad status: %+v", st)
	}
}

func TestCheckStatusSkipsUnknownConnection(t *testing.T) {
	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "42"}},
		Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "missing", ExternalId: "42"}},
		Connections: nil,
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if len(resp.GetStatuses()) != 0 {
		t.Fatalf("want 0 statuses, got %d", len(resp.GetStatuses()))
	}
}

func TestTestConnectionSuccessAndFailure(t *testing.T) {
	srv, _ := startFakeRiven(t, map[string]string{
		"__typename": `{"data":{"__typename":"Query"}}`,
	})

	ok, err := New().TestConnection(context.Background(), &pluginv1.TestConnectionRequest{
		Connection: routerConn("c1", srv.URL),
	})
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if !ok.GetOk() {
		t.Fatalf("want ok=true, got %+v", ok)
	}

	bad, err := New().TestConnection(context.Background(), &pluginv1.TestConnectionRequest{
		Connection: routerConn("c1", "http://127.0.0.1:1"),
	})
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if bad.GetOk() {
		t.Fatalf("want ok=false for unreachable host, got %+v", bad)
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]string{
		"":                   "queued",
		"Indexed":            "queued",
		"Unreleased":         "queued",
		"Scraped":            "downloading",
		"Ongoing":            "downloading",
		"PartiallyCompleted": "downloading",
		"Completed":          "completed",
		"Paused":             "failed",
		"Failed":             "failed",
	}
	for state, want := range cases {
		if got := mapState(state); got != want {
			t.Errorf("mapState(%q) = %q, want %q", state, got, want)
		}
	}
}
