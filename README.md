# Stash to GoonHub Importer

Standalone CLI tool that exports data from a [Stash](https://stashapp.cc/) instance and imports it into GoonHub.

## Quick Start

```bash
# Configure credentials and path mappings
cp .env.example .env && vi .env
cp mappings.json.example mappings.json && vi mappings.json

# Preview what will be imported (no changes made)
DRY_RUN=true go run .

# Run the import
go run .
```

## Configuration

Copy `.env.example` to `.env` and fill in your credentials. Copy `mappings.json.example` to `mappings.json` and configure path mappings between Stash and GoonHub file paths.

## Import Phases

The importer runs 5 sequential phases, saving progress to `id_map.json` after each:

1. **Tags** — matched by name (case-insensitive)
2. **Studios** — two passes (create, then set parent relationships)
3. **Performers → Actors**
4. **Scenes** — uses first file for path/metadata, tagged with `origin: "stash"`
5. **Markers** — assigned to the user specified by `GOONHUB_MARKER_USER_ID`

Re-running is safe — entities already in `id_map.json` or matched by name are skipped.

## Post-Import

```bash
# Rebuild the search index
curl -X POST http://localhost:8080/api/v1/admin/search/reindex \
  -H "Authorization: Bearer <token>"
```

Imported scenes have no thumbnails, sprites, or VTT files. Trigger processing via the GoonHub admin UI or API.

## Project Structure

- `main.go` - Entry point and orchestration
- `config.go` - Config loading from `.env` and `mappings.json`
- `stash_client.go` - Stash GraphQL API client
- `stash_types.go` - Stash response type definitions
- `goonhub_client.go` - GoonHub REST API client with retry logic
- `goonhub_types.go` - GoonHub request/response type definitions
- `importer.go` - Core import logic (5 phases)
- `id_map.go` - ID mapping persistence (JSON file)
- `path_mapper.go` - Stash → GoonHub path translation
- `schema.graphql` - Stash GraphQL schema (reference)
