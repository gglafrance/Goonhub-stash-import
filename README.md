# Stash → GoonHub importer

CLI that copies a [Stash](https://stashapp.cc/) library's metadata into GoonHub:
tags, studios (with parents), performers (with aliases, photo, rating and favorite),
scenes (with studio, tags, actors, dates, technical metadata), markers (with tags),
and the current user's interactions (rating, o-counter, view count, watch history and resume position).

**Stash is only ever read.** The client refuses to send anything other than a GraphQL
`query` (see `internal/stash/client.go`). Media files are never touched: GoonHub only
references the paths.

## Quick start

```bash
cp config.example.yaml config.yaml   # edit path_mappings
cp .env.example .env                 # secrets: STASH_API_KEY, GOONHUB_USERNAME, GOONHUB_PASSWORD...

go run . check                # read-only preflight: connectivity, storage paths, path-mapping coverage
go run . import --dry-run     # simulate everything, write nothing
go run . import --limit 20    # small batch
go run . verify --limit 20    # compare Stash and GoonHub scene by scene
go run . import               # everything (resumable, Ctrl-C safe)
go run . process              # start GoonHub processing (thumbnails, sprites) for the imported scenes
```

## Commands

| command | what it does |
|---|---|
| `import` (default) | imports the enabled phases, then triggers the search reindex (and processing if `post_import.process.enabled`) |
| `check` | read-only: Stash/GoonHub connectivity, admin role, existence of the storage paths, the number of scenes per mapping, unmapped paths |
| `verify` | for every selected scene: path, studio, tags, actors, markers and (v2) interactions. Report: `import_report.verify.json` |
| `process` | submits `post_import.process.phases` for the scenes in the state (all, or `--ids` Stash IDs) |
| `status` | per-kind counts in the state file and the latest failures |

Common flags: `--config`, `--dry-run`, `--limit N`, `--ids 1,2,3` (Stash IDs), `--on-existing skip|merge`,
`--concurrency N`, `--phases tags,studios,performers,scenes,markers,interactions`, `--log-level debug`,
`--state FILE`, `--report FILE`, `--api-mode auto|v1|v2`.

Exit codes: `0` OK, `1` errors or mismatches (details in the report), `2` configuration or connection error.

## Configuration

Everything lives in `config.yaml`. `config.example.yaml` documents every key. Highlights:

- **`path_mappings`**: Stash prefix → GoonHub prefix (as seen by the GoonHub server) + `storage_path_id`.
  The longest prefix wins and matching respects folder boundaries (`/data` never matches `/data2`).
  `check` shows the coverage.
- **`on_existing`**: `skip` (default) leaves already-imported entities alone. `merge` re-syncs those
  whose Stash data changed (a hash per entity in the state file).
- **`matching`**: reuse existing GoonHub entities by name (`name_ci`, `name`, `none`).
  `use_disambiguation` keeps Stash homonyms apart.
- **`scenes.filter`**: a Stash `SceneFilterType` passed as-is (e.g. `{organized: true}`), plus `ids` and `limit`.
- **`tags.rename` / `tags.exclude`**, **`scenes.type_rules`** (tag → GoonHub type), **`markers.color_by_tag`**.
- **`interactions`** and **`images`**: choose what to carry over.

`${VAR}` and `${VAR:-default}` are expanded from the environment / `.env`.
Without a `config.yaml`, the legacy v1 format (`.env` + `mappings.json`) is still read.

## Reliability

- **State file** (`import_state.json`): Stash ID → GoonHub ID + status (`ok`, `partial`, `failed`) for every
  entity. It is saved every 5 s and on exit, with an atomic write. A re-run only redoes what is incomplete.
  The legacy `id_map.json` is migrated automatically.
- **Without a state file** (lost or deleted), there are no duplicates. Tags, studios and performers are
  matched by name, scenes by path (409), and markers by (scene, timestamp, label).
- **Retries** with exponential backoff on network errors and 429/502/503 (and 500/504 for idempotent
  requests), automatic re-login on 401.
- **Report** (`import_report.json`): every error and warning, plus the Stash data GoonHub cannot store
  (scene code/director/urls, extra files, tag hierarchy, and marker ends when the server lacks the `marker_end` feature...).

## GoonHub import API: v1 and v2

The importer detects the server's API (`GET /api/v1/admin/import/capabilities`):

- **v2** (GoonHub with `docs/import-api.md`): one call per scene, **atomic** (scene + tags + actors +
  studio + interactions in a single transaction). Markers are deduplicated server-side and the
  summary endpoint is used for `verify`. View count and watch history are imported.
- **v1** (older GoonHub): the scene is created, then linked in several calls (a failure leaves it
  `partial` and the next run retries it). View count and watch history can't be imported in v1.

## Layout

- `main.go`: CLI
- `internal/config`: YAML config, env expansion, validation, legacy format
- `internal/stash`: read-only, paginated GraphQL client, asset download
- `internal/goonhub`: REST client (re-login, retries, pagination, v1/v2)
- `internal/importer`: phases, transformations, verify, check, report
- `internal/state`: persistent state
- `internal/pathmap`: path translation
- `internal/httpx`: retry policy
- `schema.graphql`: old Stash schema, for reference only (the live schema has evolved)
