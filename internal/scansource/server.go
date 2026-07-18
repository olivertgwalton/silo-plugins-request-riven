// Package scansource implements the scan_source.v1 capability as a sink for
// riven's outbound webhook (the riven-rs `webhooks` plugin) instead of polling
// riven's GraphQL API. On each finished download riven POSTs a JSON envelope
// to this plugin's route (proxied in by the host's http_routes.v1 plumbing);
// Handle buffers the media file paths the envelope carries and PollChanges
// drains them as file-scoped changes for the host to scan into its library.
// The payload is self-contained — riven resolves the filesystem paths before
// emitting — so no riven DB/GraphQL call is ever made.
package scansource

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// WebhookPath is the route riven's webhook plugin targets. It must match the
// `path` of the http_routes descriptor declared in manifest.json; point
// riven's webhook URL setting at the host-exposed URL for this route.
const WebhookPath = "/webhook"

// downloadSuccessEvent is the riven event slug that carries newly-available
// media file paths. Every other event on riven's notable stream is ack'd and
// ignored.
const downloadSuccessEvent = "riven.media-item.download.success"

// envelope is the subset of riven's webhook body this plugin reads. riven wraps
// the concrete event under `data`; the download-success event carries the VFS
// paths of the files it made available in `filesystem_paths`.
type envelope struct {
	Event string `json:"event"`
	Data  struct {
		FilesystemPaths []string `json:"filesystem_paths"`
	} `json:"data"`
}

// Server implements both scan_source.v1 (PollChanges) and http_routes.v1
// (Handle) over one shared buffer: Handle enqueues the media paths riven
// reports finished; PollChanges hands the accumulated set to the host and
// clears it.
//
// The pending set is process-global (guarded by mu) rather than keyed per
// installation because the http_routes Handle call carries no installation
// identity to key it by — see the single-connection limitation in the README.
type Server struct {
	pluginv1.UnimplementedScanSourceServer
	pluginv1.UnimplementedHttpRoutesServer

	mu      sync.Mutex
	pending map[string]struct{}
}

// New returns a ready-to-serve webhook-backed scan source.
func New() *Server { return &Server{pending: make(map[string]struct{})} }

// Handle receives riven's webhook. A body that isn't the JSON envelope is a
// 400; any event other than download-success (riven delivers its whole notable
// stream) is ack'd with 200 and ignored; a download-success event buffers each
// filesystem path it carries for the next PollChanges drain. The webhook is
// fire-and-forget — nothing here reaches back into riven or the host.
func (s *Server) Handle(_ context.Context, req *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	var env envelope
	if err := json.Unmarshal(req.GetBody(), &env); err != nil {
		return &pluginv1.HandleHTTPResponse{
			StatusCode: http.StatusBadRequest,
			Body:       []byte("riven webhook: body is not JSON"),
		}, nil
	}

	if env.Event == downloadSuccessEvent {
		s.mu.Lock()
		for _, path := range env.Data.FilesystemPaths {
			if path != "" {
				s.pending[path] = struct{}{}
			}
		}
		s.mu.Unlock()
	}

	return &pluginv1.HandleHTTPResponse{StatusCode: http.StatusOK}, nil
}

// PollChanges drains the media file paths accumulated from webhooks since the
// last poll and reports each as a file-scoped change. The host applies the
// source's configured path rewrites (translating riven's VFS path, e.g.
// `/mount/...`, to wherever silo's library sees it) and enqueues a scan.
//
// The host-owned marker is unused: a webhook delivery, not a diff against
// riven's state, is what makes a path "new", so there is no continuation token
// to carry between polls. Draining is destructive — a path reported once is
// not reported again — so a lost PollChanges response drops that batch
// (accepted trade-off of the webhook-only model; a subsequent completion or a
// manual library scan reconciles it).
func (s *Server) PollChanges(_ context.Context, _ *pluginv1.PollChangesRequest) (*pluginv1.PollChangesResponse, error) {
	s.mu.Lock()
	paths := make([]string, 0, len(s.pending))
	for path := range s.pending {
		paths = append(paths, path)
	}
	s.pending = make(map[string]struct{})
	s.mu.Unlock()

	sort.Strings(paths)
	changes := make([]*pluginv1.ScanSourceChange, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, &pluginv1.ScanSourceChange{
			SourcePath: path,
			Scope:      pluginv1.ScanSourceChangeScope_SCAN_SOURCE_CHANGE_SCOPE_FILE,
		})
	}
	return &pluginv1.PollChangesResponse{Changes: changes}, nil
}
