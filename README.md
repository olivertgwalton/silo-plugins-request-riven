# Silo Requests: Riven

A Silo plugin that connects to a [riven-rs](https://github.com/rivenmedia/riven-rs)
instance's GraphQL API for two capabilities:

- **`request_router.v1`**: fulfills content requests directly against riven's
  native request API (`requestMovie` / `requestShow`). Unlike the
  [Seerr plugin](https://github.com/Silo-Community/silo-plugins-requests-seerr),
  no Seerr instance is involved: riven tracks and scrapes the request itself.
- **`scan_source.v1`** (+ **`http_routes.v1`**): receives riven's outbound
  webhook when a download finishes and reports the file paths it carries so
  Silo's autoscan engine scans them into the library — no polling of riven's
  DB/GraphQL. See [Scan source](#scan-source) below.

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

Instead of polling riven's DB/GraphQL, the plugin is driven by riven's own
outbound webhook (the riven-rs `webhooks` plugin). It registers an HTTP route —
`POST /webhook` — that the host exposes; point riven's webhook URL setting at
that route's public URL. On every notable state change riven POSTs a signed
JSON envelope; on `riven.media-item.download.success` the envelope carries the
VFS paths of the files the download made available, under
`data.filesystem_paths`. The plugin ignores every other event and buffers those
paths.

> **Requires riven-rs with the `filesystem_paths` field.** Stock riven-rs
> emits `download.success` without the file paths, so the envelope alone can't
> drive a scan. This plugin pairs with a riven-rs build whose
> `MediaItemDownloadSuccess` event includes `filesystem_paths` (resolved from
> `filesystem_entries` at emit time — a season/show carries every child
> episode's path). Against stock riven-rs the webhook is received and ack'd but
> reports no changes.

Silo drains the buffer on the scan source's poll tick: each buffered path is
returned as a `file`-scoped change, and Silo applies the source's configured
path rewrites (translating riven's VFS path, e.g. `/mount/...`, to wherever
Silo's library actually sees that path) before enqueuing a scan. The drain is
destructive — a path is reported once — and there is no continuation marker,
because webhook delivery (not a diff against riven's state) is what makes a
path "new". Nothing is replayed when a source is first bound: only downloads
that finish afterward are scanned.

### Setup

1. In riven, configure the `webhooks` plugin: add the host-exposed URL for this
   plugin's `/webhook` route to **Webhook URLs**, and (optionally) narrow the
   **Event Filter** to `riven.media-item.download.success`.
2. In Silo's Plugins page, install this scan source and set a poll interval
   (the default applies if left unset) — the interval only governs how quickly
   buffered webhooks are drained, not how riven is queried.

**Security:** the `/webhook` route is declared with `public` access so riven
(an external caller with no Silo session) can reach it. The plugin does not see
riven's webhook signing secret, so it can't verify the `x-riven-signature`
header — protect the route at the network / reverse-proxy layer.

**Known limitations:**

- **Single connection.** The webhook carries no installation identity, so
  buffered paths are process-global. With more than one bound Riven scan
  source, a webhook is delivered to whichever source drains first; run a
  single scan source per Silo instance.
- **Deletions are not detected.** riven's deletion event is not consumed; if
  riven removes a completed item's files, this source will not notice. Run a
  manual or scheduled library scan to reconcile removals.
- **No restart recovery.** A webhook that arrives while the plugin is down is
  not retried; the next completion or a manual scan reconciles it.

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
