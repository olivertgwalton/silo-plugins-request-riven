// Package riven is a minimal client for the subset of riven-rs's GraphQL API
// used to submit and poll content requests: the requestMovie/requestShow
// mutations and the mediaItemByTmdb/Imdb/Tvdb queries. It carries no
// credentials; every call takes its own httpclient.Client (base URL + API
// key), matching the request_router.v1 contract of stateless, per-call
// connections.
package riven

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

const graphQLPath = "/graphql"

type gqlRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlEnvelope[T any] struct {
	Data   T          `json:"data"`
	Errors []gqlError `json:"errors,omitempty"`
}

// do posts a GraphQL query/mutation and decodes its "data" field into T. A
// transport-level failure (network, non-2xx, auth) surfaces as
// *httpclient.StatusError from PostJSON; a GraphQL-level failure (a 200
// response carrying an "errors" array) surfaces as a plain error.
func do[T any](ctx context.Context, client *httpclient.Client, query string, variables any) (T, error) {
	var envelope gqlEnvelope[T]
	if err := client.PostJSON(ctx, graphQLPath, gqlRequest{Query: query, Variables: variables}, &envelope); err != nil {
		var zero T
		return zero, err
	}
	if len(envelope.Errors) > 0 {
		var zero T
		return zero, fmt.Errorf("riven: %s", envelope.Errors[0].Message)
	}
	return envelope.Data, nil
}

// Ping verifies the connection is reachable and the API key is accepted.
func Ping(ctx context.Context, client *httpclient.Client) error {
	type data struct {
		Typename string `json:"__typename"`
	}
	_, err := do[data](ctx, client, `query { __typename }`, nil)
	return err
}

// RequestOutcome is the normalized result of a requestMovie/requestShow call.
type RequestOutcome struct {
	Success  bool
	Message  string
	Conflict bool
	// State is the created/existing item request's state, e.g. "REQUESTED".
	// Empty when the mutation reported a conflict (riven returns item: null).
	State string
}

type itemRequestFragment struct {
	State string `json:"state"`
}

type requestMutationResult struct {
	Success bool                 `json:"success"`
	Message string               `json:"message"`
	Item    *itemRequestFragment `json:"item"`
}

func toOutcome(r requestMutationResult) RequestOutcome {
	outcome := RequestOutcome{Success: r.Success, Message: r.Message}
	if r.Item != nil {
		outcome.State = r.Item.State
	} else if !r.Success {
		// The only case request_movie/request_show returns success: false with
		// no item is an unchanged (duplicate) request; hard errors are
		// surfaced as a GraphQL error instead, not a false-success payload.
		outcome.Conflict = true
	}
	return outcome
}

const requestMovieMutation = `
mutation($input: MovieRequestInput!) {
  requestMovie(input: $input) {
    success
    message
    item { state }
  }
}`

// RequestMovie submits a movie request. imdbID/tmdbID/requestedBy may be nil;
// at least one of imdbID/tmdbID must be set for riven to accept the request.
func RequestMovie(ctx context.Context, client *httpclient.Client, title string, imdbID, tmdbID, requestedBy *string) (RequestOutcome, error) {
	input := map[string]any{"title": title}
	setIfNotNil(input, "imdbId", imdbID)
	setIfNotNil(input, "tmdbId", tmdbID)
	setIfNotNil(input, "requestedBy", requestedBy)

	type data struct {
		RequestMovie requestMutationResult `json:"requestMovie"`
	}
	result, err := do[data](ctx, client, requestMovieMutation, map[string]any{"input": input})
	if err != nil {
		return RequestOutcome{}, err
	}
	return toOutcome(result.RequestMovie), nil
}

const requestShowMutation = `
mutation($input: ShowRequestInput!) {
  requestShow(input: $input) {
    success
    message
    item { state }
  }
}`

// RequestShow submits a show request for all non-special seasons. imdbID,
// tvdbID and requestedBy may be nil; at least one of imdbID/tvdbID must be set.
func RequestShow(ctx context.Context, client *httpclient.Client, title string, imdbID, tvdbID, requestedBy *string) (RequestOutcome, error) {
	input := map[string]any{"title": title}
	setIfNotNil(input, "imdbId", imdbID)
	setIfNotNil(input, "tvdbId", tvdbID)
	setIfNotNil(input, "requestedBy", requestedBy)

	type data struct {
		RequestShow requestMutationResult `json:"requestShow"`
	}
	result, err := do[data](ctx, client, requestShowMutation, map[string]any{"input": input})
	if err != nil {
		return RequestOutcome{}, err
	}
	return toOutcome(result.RequestShow), nil
}

func setIfNotNil(m map[string]any, key string, v *string) {
	if v != nil {
		m[key] = *v
	}
}

type mediaItemState struct {
	State *string `json:"state"`
}

// MediaItemState looks up a media item's state by its external id. It returns
// ("", nil) when no such media item exists yet (the request is still queued
// upstream of indexing).
func MediaItemState(ctx context.Context, client *httpclient.Client, idKind, id string) (string, error) {
	var query string
	switch idKind {
	case "tmdb":
		query = `query($id: String!) { mediaItemByTmdb(tmdbId: $id) { state } }`
	case "tvdb":
		query = `query($id: String!) { mediaItemByTvdb(tvdbId: $id) { state } }`
	case "imdb":
		query = `query($id: String!) { mediaItemByImdb(imdbId: $id) { state } }`
	default:
		return "", fmt.Errorf("riven: unknown id kind %q", idKind)
	}

	// The field name varies by idKind, so decode into a map instead of a
	// fixed struct.
	raw, err := do[map[string]*mediaItemState](ctx, client, query, map[string]any{"id": id})
	if err != nil {
		return "", err
	}
	for _, v := range raw {
		if v != nil && v.State != nil {
			return *v.State, nil
		}
	}
	return "", nil
}
