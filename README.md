# Silo Requests: Riven

A Silo plugin that connects to a [riven-rs](https://github.com/rivenmedia/riven-rs)
instance's GraphQL API for two capabilities:

- **`request_router.v1`**: fulfills content requests directly against riven's
  native request API (`requestMovie` / `requestShow`). Unlike the
  [Seerr plugin](https://github.com/Silo-Community/silo-plugins-requests-seerr),
  no Seerr instance is involved: riven tracks and scrapes the request itself.
- **`scan_source.v1`**: detects movies and episodes riven-rs has finished
  downloading and reports their filesystem paths so Silo's autoscan engine
  scans them into the library. See [Scan source](#scan-source) below.

Both capabilities can be bound to the same Riven connection.

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

## Scan source

Install the plugin, then from Silo's Plugins page install this scan source,
bind it to the same Riven connection used for requests, and set a poll
interval (the default applies if left unset).

On each poll, the plugin asks riven for every `Movie` and `Episode` currently
in the `Completed` state, diffs that against the set of ids it has already
reported (the host-owned opaque marker), and returns the filesystem path of
each newly-completed item as a `file`-scoped change. Silo applies the
source's configured path rewrites (translating riven's VFS path, e.g.
`/mount/...`, to wherever Silo's library actually sees that path) and
enqueues a scan.

A source's first poll never replays riven's existing library: it seeds the
marker with every already-completed item and reports no changes, matching
the host's "start from now" contract for a freshly bound source.

**Known limitation:** deletions are not detected. If riven removes a
completed item's files, this source will not notice; run a manual or
scheduled library scan to reconcile removals.

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
