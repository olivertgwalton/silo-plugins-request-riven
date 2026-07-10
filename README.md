# Silo Requests: Riven

A Silo `request_router.v1` plugin that fulfills content requests directly
against a [riven-rs](https://github.com/rivenmedia/riven-rs) instance's native
GraphQL request API (`requestMovie` / `requestShow`). Unlike the
[Seerr plugin](https://github.com/Silo-Community/silo-plugins-requests-seerr),
no Seerr instance is involved: riven tracks and scrapes the request itself.

## Connection config

Each connection carries a riven **base URL** (e.g. `http://localhost:8080`)
and **API key** (host chrome) — the value of `RIVEN_SETTING__API_KEY` on that
riven-rs instance. There are no additional plugin settings: riven has no
per-connection quality profile or requester-mapping concept.

## API key requirement

The API key must match riven's `RIVEN_SETTING__API_KEY`. Non-frontend GraphQL
callers (anything without riven's frontend-signing headers) that present a
valid key are treated as trusted/admin, so every request submitted by this
plugin is accepted immediately — riven has no per-user approval queue.
`TestConnection` runs a minimal `{ __typename }` query to surface an invalid
key or unreachable host.

## How requests map

- Movies call `requestMovie`; series call `requestShow` (all non-special
  seasons — riven's request descriptor has no per-season field to narrow
  this). Media is identified by **TMDB id** (movies) or **TVDB id** (series),
  falling back to **IMDB id** for either.
- Riven has no per-quality profile distinction — its own scraper/ranking
  config picks the best available release — so the request is submitted to
  riven exactly once per connection regardless of how many quality tiers
  Silo asks for. Silo requires one `FulfillmentTarget` per requested quality
  (a tier with no matching target gets marked failed), so that single
  outcome is echoed back once per tier, all sharing the same external id
  and status.
- A duplicate request (riven returns `success: false` with no `item`) is
  treated as already-queued.
- `requested_by` is set to the literal string `"silo"`, matching riven's own
  doc comment that the field identifies the *originating system*, not the
  individual requester — riven has no per-user identity to attribute to.
- `CheckStatus` re-queries `mediaItemByTmdb` / `mediaItemByTvdb` /
  `mediaItemByImdb` for the item's current `state` and maps riven's state
  machine (`Indexed`, `Unreleased`, `Scraped`, `Ongoing`,
  `PartiallyCompleted`, `Completed`, `Paused`, `Failed`) onto Silo's
  normalized `queued` / `downloading` / `completed` / `failed`.

## Build / test

```
go build ./... && go test ./...
```

## Maintenance

This plugin follows the same structure as the community-maintained
[`silo-plugins-requests-seerr`](https://github.com/Silo-Community/silo-plugins-requests-seerr),
but is maintained independently in this repo. Security reports should follow
[`SECURITY.md`](SECURITY.md).

The plugin consumes the published
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk); CI
rejects machine-local SDK replacement directives.

## License

Licensed under AGPL-3.0. See [`LICENSE`](LICENSE).
