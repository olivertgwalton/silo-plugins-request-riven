// Package router implements the request_router.v1 RPCs over a riven-rs
// backend. The Server holds no state and stores no credentials; every call
// carries its own connections.
package router

import (
	"context"
	"errors"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/olivertgwalton/silo-plugins-request-riven/internal/riven"
)

// Server implements the request_router.v1 RPCs. It is stateless.
type Server struct {
	pluginv1.UnimplementedRequestRouterServer
}

// New returns a ready-to-serve request router.
func New() *Server { return &Server{} }

// requestedBySilo is the value riven's requested_by field records: the
// identifier of the external system that originated the request, per its own
// doc comment ("e.g. Seerr"). Riven has no per-user approval queue, so
// individual requester identity is not attributed further.
var requestedBySilo = ptr("silo")

func ptr(s string) *string { return &s }

func nonEmpty(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	return s, true
}

// externalID picks the id riven needs to identify the request: TVDB for
// series (falling back to IMDB), TMDB for movies (falling back to IMDB).
// Returns an error when none of the ids riven understands are present.
func externalID(d *pluginv1.RequestDescriptor) (idKind, id string, err error) {
	ids := d.GetExternalIds()
	imdbID, hasIMDB := nonEmpty(ids["imdb"])

	if d.GetMediaType() == "series" {
		if tvdbID, ok := nonEmpty(ids["tvdb"]); ok {
			return "tvdb", tvdbID, nil
		}
		if hasIMDB {
			return "imdb", imdbID, nil
		}
		return "", "", errors.New("request has no TVDB or IMDB id; riven requires one to track a series request")
	}

	if tmdbID, ok := nonEmpty(ids["tmdb"]); ok {
		return "tmdb", tmdbID, nil
	}
	if hasIMDB {
		return "imdb", imdbID, nil
	}
	return "", "", errors.New("request has no TMDB or IMDB id; riven requires one to track a movie request")
}

// imdbAndOther splits an (idKind, id) pair into the (imdbID, tmdbID/tvdbID)
// pointer pair the riven mutations take, leaving whichever id riven doesn't
// use for this kind as nil.
func imdbAndOther(idKind, id string) (imdbID, other *string) {
	if idKind == "imdb" {
		return ptr(id), nil
	}
	return nil, ptr(id)
}

// Fulfill submits exactly one riven item request per connection: riven has no
// per-quality profile distinction (its own scraper/ranking picks the best
// available release), so every requested quality tier maps onto the same
// underlying request. The first quality's id is echoed back so the host can
// still track the target; per-target containment still applies across
// connections (one failing connection never aborts the others).
func (s *Server) Fulfill(ctx context.Context, req *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	d := req.GetRequest()
	isSeries := d.GetMediaType() == "series"

	idKind, id, idErr := externalID(d)
	if idErr != nil {
		// One request-level failure, not N identical per-target failures.
		return &pluginv1.FulfillResponse{Message: idErr.Error()}, nil
	}
	imdbID, other := imdbAndOther(idKind, id)

	quality := "any"
	if qs := req.GetQualities(); len(qs) > 0 {
		quality = qs[0].GetId()
	}

	var targets []*pluginv1.FulfillmentTarget
	for _, c := range req.GetConnections() {
		conn := connectionFromRouter(c)
		client := httpclient.New(conn.BaseURL, conn.APIKey, nil)
		targets = append(targets, s.fulfillOne(ctx, client, conn.ID, quality, id, isSeries, d.GetTitle(), imdbID, other))
	}
	if len(targets) == 0 {
		return &pluginv1.FulfillResponse{Message: "no riven connection configured"}, nil
	}
	return &pluginv1.FulfillResponse{Targets: targets}, nil
}

// fulfillOne submits a single connection's request.
func (s *Server) fulfillOne(ctx context.Context, client *httpclient.Client, connID, quality, extID string, isSeries bool, title string, imdbID, other *string) *pluginv1.FulfillmentTarget {
	t := &pluginv1.FulfillmentTarget{Quality: quality, ConnectionId: connID, ExternalId: extID}

	var outcome riven.RequestOutcome
	var err error
	if isSeries {
		outcome, err = riven.RequestShow(ctx, client, title, imdbID, other, requestedBySilo)
	} else {
		outcome, err = riven.RequestMovie(ctx, client, title, imdbID, other, requestedBySilo)
	}

	switch {
	case err != nil:
		t.Status = "failed"
		t.Message = err.Error()
	case outcome.Conflict:
		t.Status = "queued"
		t.Message = "already requested in riven"
	default:
		t.Status = "queued"
		t.Message = outcome.Message
		t.ExternalStatus = outcome.State
	}
	return t
}

// CheckStatus re-derives the request's external id from the descriptor (the
// same one Fulfill used) and looks up the resulting media item's state per
// connection, mapping riven's state machine onto the host's normalized
// "queued" | "downloading" | "completed" | "failed" status. Targets whose
// connection is missing, or whose lookup errors, are skipped so one
// unreachable connection does not blank the whole response.
func (s *Server) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	idKind, id, idErr := externalID(req.GetRequest())
	if idErr != nil {
		return &pluginv1.CheckStatusResponse{}, nil
	}

	byID := make(map[string]*httpclient.Client, len(req.GetConnections()))
	for _, c := range req.GetConnections() {
		conn := connectionFromRouter(c)
		byID[conn.ID] = httpclient.New(conn.BaseURL, conn.APIKey, nil)
	}

	var statuses []*pluginv1.TargetStatus
	for _, tref := range req.GetTargets() {
		client, ok := byID[tref.GetConnectionId()]
		if !ok {
			continue
		}
		state, err := riven.MediaItemState(ctx, client, idKind, id)
		if err != nil {
			continue // transient error: skip, retry next cycle
		}
		statuses = append(statuses, &pluginv1.TargetStatus{
			Quality:        tref.GetQuality(),
			ConnectionId:   tref.GetConnectionId(),
			Status:         mapState(state),
			ExternalStatus: state,
		})
	}
	return &pluginv1.CheckStatusResponse{Statuses: statuses}, nil
}

// mapState maps riven's MediaItemState GraphQL enum values onto the host's
// normalized status. An empty state means no media item has been indexed for
// the request yet, i.e. still queued.
func mapState(state string) string {
	switch state {
	case "Completed":
		return "completed"
	case "Failed", "Paused":
		return "failed"
	case "Scraped", "PartiallyCompleted", "Ongoing":
		return "downloading"
	default: // "Indexed", "Unreleased", ""
		return "queued"
	}
}

// TestConnection verifies the base URL + API key via a minimal GraphQL query.
// Never returns a gRPC error; failure is Ok:false + message.
func (s *Server) TestConnection(ctx context.Context, req *pluginv1.TestConnectionRequest) (*pluginv1.TestConnectionResponse, error) {
	conn := connectionFromRouter(req.GetConnection())
	if err := riven.Ping(ctx, httpclient.New(conn.BaseURL, conn.APIKey, nil)); err != nil {
		return &pluginv1.TestConnectionResponse{Ok: false, Message: err.Error()}, nil
	}
	return &pluginv1.TestConnectionResponse{Ok: true, Message: "connection successful"}, nil
}

// ListConfigOptions returns no dynamic options: riven's connection config has
// no dynamic-options fields. Returned empty (not Unimplemented) so the host's
// options probe gets a clean answer.
func (s *Server) ListConfigOptions(ctx context.Context, req *pluginv1.ListConfigOptionsRequest) (*pluginv1.ListConfigOptionsResponse, error) {
	return &pluginv1.ListConfigOptionsResponse{}, nil
}

// Validate has no cross-field rules to check: the connection config has no
// fields at all. Returned empty so the host's save-time Validate succeeds.
func (s *Server) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	return &pluginv1.ValidateResponse{}, nil
}
