// Package scansource implements the scan_source.v1 RPC over a riven-rs
// backend: it polls for media items riven has finished downloading and
// reports their filesystem paths so the host can scan them into its library.
package scansource

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/olivertgwalton/silo-plugins-request-riven/internal/riven"
)

// Server implements the scan_source.v1 RPCs. It is stateless: the only state
// (which riven media items have already been reported) lives in the
// host-owned marker round-tripped on every call.
type Server struct {
	pluginv1.UnimplementedScanSourceServer
}

// New returns a ready-to-serve scan source.
func New() *Server { return &Server{} }

// completedItemTypes are the riven MediaItemType values that carry their own
// filesystem entries. Season/Show are aggregate rollups with no files of
// their own; a season pack still completes as individual Episode items.
var completedItemTypes = []string{"MOVIE", "EPISODE"}

// marker is the opaque PollChanges continuation token: every riven media item
// id already reported to the host as Completed.
type marker struct {
	IDs []int64 `json:"ids"`
}

func decodeMarker(raw string) (map[int64]struct{}, error) {
	seen := make(map[int64]struct{})
	if raw == "" {
		return seen, nil
	}
	var m marker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("riven scan_source: decode marker: %w", err)
	}
	for _, id := range m.IDs {
		seen[id] = struct{}{}
	}
	return seen, nil
}

func encodeMarker(seen map[int64]struct{}) (string, error) {
	m := marker{IDs: make([]int64, 0, len(seen))}
	for id := range seen {
		m.IDs = append(m.IDs, id)
	}
	sort.Slice(m.IDs, func(i, j int) bool { return m.IDs[i] < m.IDs[j] })
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("riven scan_source: encode marker: %w", err)
	}
	return string(b), nil
}

// PollChanges reports the filesystem path of every riven media item that
// reached the Completed state since the last poll. A source's first poll
// (empty marker) seeds the marker with every already-completed item and
// reports no changes: the host's contract is "start from now", not a replay
// of riven's entire existing library.
func (s *Server) PollChanges(ctx context.Context, req *pluginv1.PollChangesRequest) (*pluginv1.PollChangesResponse, error) {
	firstRun := req.GetMarker() == ""
	previouslySeen, err := decodeMarker(req.GetMarker())
	if err != nil {
		return nil, err
	}

	conn := req.GetConnection()
	client := httpclient.New(conn.GetBaseUrl(), conn.GetApiKey(), nil)

	current := make(map[int64]struct{})
	var changes []*pluginv1.ScanSourceChange
	for _, itemType := range completedItemTypes {
		ids, err := riven.CompletedItemIDs(ctx, client, itemType)
		if err != nil {
			return nil, fmt.Errorf("riven scan_source: list completed %s items: %w", itemType, err)
		}
		for _, id := range ids {
			current[id] = struct{}{}
			if firstRun {
				continue
			}
			if _, ok := previouslySeen[id]; ok {
				continue
			}
			paths, err := riven.FilesystemPaths(ctx, client, id)
			if err != nil {
				return nil, fmt.Errorf("riven scan_source: filesystem entries for item %d: %w", id, err)
			}
			for _, path := range paths {
				changes = append(changes, &pluginv1.ScanSourceChange{
					SourcePath: path,
					Scope:      pluginv1.ScanSourceChangeScope_SCAN_SOURCE_CHANGE_SCOPE_FILE,
				})
			}
		}
	}

	nextMarker, err := encodeMarker(current)
	if err != nil {
		return nil, err
	}
	return &pluginv1.PollChangesResponse{
		Changes:    changes,
		NextMarker: nextMarker,
	}, nil
}
