package scansource

import (
	"context"
	"net/http"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// post sends a raw body to Handle and returns the response.
func post(t *testing.T, srv *Server, body string) *pluginv1.HandleHTTPResponse {
	t.Helper()
	resp, err := srv.Handle(context.Background(), &pluginv1.HandleHTTPRequest{
		Method:  http.MethodPost,
		Path:    WebhookPath,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(body),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return resp
}

// drain runs a PollChanges and returns the file paths it reported.
func drain(t *testing.T, srv *Server) []string {
	t.Helper()
	resp, err := srv.PollChanges(context.Background(), &pluginv1.PollChangesRequest{})
	if err != nil {
		t.Fatalf("PollChanges: %v", err)
	}
	paths := make([]string, 0, len(resp.GetChanges()))
	for _, c := range resp.GetChanges() {
		if c.GetScope() != pluginv1.ScanSourceChangeScope_SCAN_SOURCE_CHANGE_SCOPE_FILE {
			t.Fatalf("want file scope, got %v for %q", c.GetScope(), c.GetSourcePath())
		}
		paths = append(paths, c.GetSourcePath())
	}
	return paths
}

func TestDownloadSuccessPathsAreReportedThenDrained(t *testing.T) {
	srv := New()

	resp := post(t, srv, `{
		"event": "riven.media-item.download.success",
		"media_item_id": 42,
		"data": {
			"type": "riven.media-item.download.success",
			"id": 42,
			"filesystem_paths": ["/movies/Arrival (2016)/Arrival (2016).mkv"]
		}
	}`)
	if resp.GetStatusCode() != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.GetStatusCode())
	}

	got := drain(t, srv)
	if len(got) != 1 || got[0] != "/movies/Arrival (2016)/Arrival (2016).mkv" {
		t.Fatalf("bad drained paths: %+v", got)
	}

	// The buffer is drained destructively: a second poll reports nothing.
	if again := drain(t, srv); len(again) != 0 {
		t.Fatalf("second drain should be empty, got %+v", again)
	}
}

func TestSeasonPackReportsEveryEpisodePathDeduped(t *testing.T) {
	srv := New()

	post(t, srv, `{
		"event": "riven.media-item.download.success",
		"data": { "filesystem_paths": [
			"/shows/Severance/Season 01/S01E01.mkv",
			"/shows/Severance/Season 01/S01E02.mkv"
		] }
	}`)
	// A redelivery of an overlapping path must not double-report it.
	post(t, srv, `{
		"event": "riven.media-item.download.success",
		"data": { "filesystem_paths": ["/shows/Severance/Season 01/S01E02.mkv"] }
	}`)

	got := drain(t, srv)
	if len(got) != 2 ||
		got[0] != "/shows/Severance/Season 01/S01E01.mkv" ||
		got[1] != "/shows/Severance/Season 01/S01E02.mkv" {
		t.Fatalf("want both episodes once, sorted; got %+v", got)
	}
}

func TestNonDownloadEventIsAckedAndIgnored(t *testing.T) {
	srv := New()

	// riven delivers its whole notable stream; a scrape-success event carries
	// no paths and must be ack'd without buffering anything.
	resp := post(t, srv, `{
		"event": "riven.media-item.scrape.success",
		"data": { "id": 7, "title": "X" }
	}`)
	if resp.GetStatusCode() != http.StatusOK {
		t.Fatalf("want 200 ack, got %d", resp.GetStatusCode())
	}
	if got := drain(t, srv); len(got) != 0 {
		t.Fatalf("scrape event must buffer nothing, got %+v", got)
	}
}

func TestDownloadSuccessWithNoPathsBuffersNothing(t *testing.T) {
	srv := New()

	resp := post(t, srv, `{"event":"riven.media-item.download.success","data":{"filesystem_paths":[]}}`)
	if resp.GetStatusCode() != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.GetStatusCode())
	}
	if got := drain(t, srv); len(got) != 0 {
		t.Fatalf("want nothing buffered, got %+v", got)
	}
}

func TestNonJSONBodyIsBadRequest(t *testing.T) {
	srv := New()

	resp := post(t, srv, `not json`)
	if resp.GetStatusCode() != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.GetStatusCode())
	}
	if got := drain(t, srv); len(got) != 0 {
		t.Fatalf("nothing should be buffered, got %+v", got)
	}
}

func TestPollChangesEmptyBufferReportsNothing(t *testing.T) {
	if got := drain(t, New()); len(got) != 0 {
		t.Fatalf("empty source should report no changes, got %+v", got)
	}
}
