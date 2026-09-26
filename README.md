# stl-library

A lightweight, self-hosted manager for 3D-printing libraries (STL, Lychee,
Chitubox) that shows your collection as **Model → Variants → Parts**: a
model usually comes split into parts, and in several variants (scales,
supported/unsupported, hollow/solid, slicer formats).

Designed for small home servers: a single static Go binary, SQLite, low
memory, and low CPU/I/O priority for anything that touches the disk.

**The app never moves, renames or deletes your files.** (The optional
importer only ever *adds* new files, see below.) It expects your
library to follow the folder convention below, reads conforming folders
exactly, and lists folders that don't conform so you can fix them yourself.

Features:

- Library grid with search (name, creator, release, category, tags) and
  filters (creator, tag, printed / never printed).
- Model page: pick a variant by its dimensions (`75mm · Supported ·
  Hollow`), images, parts, **download the variant as a zip**, a **3D view**
  of any STL/OBJ/3MF part.
- Previews: thumbnails of bundled images; models without an image get a
  **rendered preview of their STL** (made in the background, cached).
- Your own data on top: **tags**, a **display name** (the folder stays as it
  is), **print history** per variant and a **print queue**. Kept by folder,
  so it survives rescans.
- "Not following the convention": the folders the app can't read, and why.
- Corrections without touching the files: hide a model, fix a variant's
  label (e.g. a folder the app read as "No Supports" that is supported).
- **Import from a downloads folder** (optional): new, fully downloaded
  models are copied into the library in the convention's layout, zip
  archives unpacked.
- `stlib check` / `stlib scan` / `stlib search` on the command line.

## Folder convention

```
<Creator>/<Release>/[<Category>]/<Model>/
    images, readme                                 <- model-level files
    [<Scale>]/                                     <- 32mm, 75mm, Bust, 1-12, …
        <Supports>[ <Density>][ <Format>][ Combined]/
            [Hollow|Solid]/
                [<Option>/]                        <- e.g. "Helmet Version"
                    part files
```

- **Levels in brackets are optional**: leave out what a model doesn't have.
  A model without any variant information keeps its files directly in the
  model folder.
- **Canonical spellings** (the exact folder names that are recognized):
  - Scale: `32mm`, `75mm`, … `Bust`, `1-12`, `Freescale`, `Heroic`
  - Supports: `Supported` or `No Supports`, optionally followed by a density
    (`Beefed`, `Light`), a format (`Lychee`, `Chitubox`, `STL`), `Combined`
    for one-piece files, and `Repaired`/`Original`, e.g. `Supported Lychee`,
    `No Supports Combined`
  - Fill: `Hollow`, `Solid`
  - Tech: `FDM`, `Resin`
- **Categories** optionally group models inside a release: `Heroes`,
  `Enemies`, `Busts`, `Environments`, `Objects`, …
- **Options** (a helmet version, an alternative pose) are a subfolder inside
  the variant, not a model of their own. A model without variant levels
  can have options directly below it (`Stonewurm Riders/Pose01`): any level
  deeper than `<Creator>/<Release>/[<Category>]/<Model>` is an option.
- **Levels are read by position.** A model may sit directly under its
  creator (`<Creator>/<Model>`), or under a creator-level category
  (`<Creator>/Busts/<Model>`). A category is recognized by its canonical
  name.
- Folders starting with `_` (e.g. `_duplicates`) and hidden files are not
  part of the library.

Example: `Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported Lychee/`

`convention.CanonicalSegments` is the single source of truth for how a
variant is spelled; `convention.ParseSegment` reads a canonical folder name
back, and accepts nothing else. The package is public and has no
dependencies, so a migration script for an existing library can import it
to produce folders the app recognizes.

## Architecture

Hexagonal. The dependency rule is enforced by `internal/architecture_test.go`:

| Layer | Packages | May depend on |
|---|---|---|
| convention (public, pure) | `convention` | standard library only, no I/O |
| domain (pure) | `internal/core/library` (reads a file listing into models, variants, parts and issues), `internal/core/thumb` (image thumbnails), `internal/core/render` (streaming STL rasterizer) | convention |
| use cases + ports | `internal/app` (`Check`, `Scan`, `Search`, `VariantZip`, `Thumbs`, `UserData`; ports `Lister`, `Store`, `Files`, `ThumbCache`) | core |
| adapters | `internal/adapters/disk` (read-only listing and file access, thumbnail cache), `internal/adapters/sqlite` (index, FTS5, user data), `internal/adapters/httpapi` (JSON API, driving), `internal/adapters/web` (embedded UI) | app, core |
| driving adapter | `cmd/stlib` | everything, wiring only |

## Usage

```
stlib check /path/to/library
```

Prints how many models, variants and part files were found per creator,
then every folder that doesn't follow the convention and why. Runs at the
lowest CPU and I/O priority and only reads.

```
stlib scan --db index.db /path/to/library
stlib search --db index.db [--creator "Loot Studios"] [--limit 50] bell head
```

`scan` brings the index up to date. Models are matched by folder and only
rewritten when their content changed (files, sizes, modification times), so
a rescan of an unchanged library writes nothing and a model keeps its id
while its folder stays. `search` matches every word as a prefix against
model name, creator, release and category (diacritics ignored).

### Importing new downloads

With `IMPORT_SOURCE` set, `stlib serve` looks at that folder every
`IMPORT_INTERVAL` and copies new download folders into the library:

- **Layout:** `<downloads>/<creator>/<model>/…` (or a container folder of
  models, like "Last month's models"). The creator maps to an existing
  creator folder of the library when the names match ignoring case,
  otherwise a new one is made.
- **Only complete downloads.** A folder is copied once it has no temporary
  download files (`*.partial`, `*.part`, `*.crdownload`, hidden temp files
  like rsync's), nothing in it has changed for `IMPORT_SETTLE` (judged by
  the file system's change time, which downloaders can't set back), and
  every zip in it opens. Anything else waits for the next run.
- **Where files go:** variant information is read from folder, archive and
  file names (`…_pre_supported_lys.zip`, `(Chitubox Pre Supported)`,
  `Presupports/1-10 Scale_Split`), zips are unpacked, images and documents
  go next to the model. A wrong guess can be fixed in the UI (fix label).
- **Never destructive:** the downloads are only read; the library only gets
  new files (written under a hidden temporary name, then renamed). A file
  that exists with a different size is kept, the new one is added as
  "… (imported)".
- **The first run imports nothing:** it records what's already in the
  downloads folder (it may have been copied by hand before). Those
  folders can be imported one by one from the Imports page; everything
  that arrives later is imported automatically. When new files arrive in
  an imported folder, the missing ones are copied.

`stlib import --db index.db --source <downloads> --dry-run <library>` shows
where every file would go without writing anything.

### Server

```
LIBRARY_ROOT=/path/to/library DATA_DIR=./data stlib serve
```

| Variable | Default | |
|---|---|---|
| `LIBRARY_ROOT` | (required) | the library; only read, never written |
| `DATA_DIR` | `./data` | the index (`index.db`) |
| `LISTEN_ADDR` | `127.0.0.1:8080` | |
| `SCAN_INTERVAL` | `1h` | time between rescans (at least `1m`); the first scan starts right away |
| `IMPORT_SOURCE` | (off) | downloads folder to import from; the library must then be writable |
| `IMPORT_INTERVAL` | `1h` | time between imports (at least `1m`) |
| `IMPORT_SETTLE` | `1h` | how long a download folder must be unchanged to count as complete |

Each has a flag of the same meaning (`--root`, `--data`, `--listen`,
`--scan-interval`).

API:

| | |
|---|---|
| `GET /api/models?q=&creator=&limit=&offset=` | search / list models |
| `GET /api/models/{id}` | a model with its variants (dimensions, option, parts) and images |
| `GET /api/creators` | creators with their model counts |
| `GET /api/issues` | folders that don't follow the convention |
| `GET /api/variants/{id}/zip` | a variant's parts as a zip (stored uncompressed, streamed) |
| `GET /api/parts/{id}`, `GET /api/images/{id}` | single files |
| `GET /api/images/{id}/thumb`, `GET /api/models/{id}/thumb` | image thumbnail; model preview (cover thumbnail or STL render) |
| `GET /api/tags`, `PUT /api/models/{id}/tags` | tags with counts; replace a model's tags (`{"tags": [...]}`) |
| `PUT /api/models/{id}/name` | display name (`{"name": "..."}`, empty = folder name) |
| `POST /api/variants/{id}/prints`, `DELETE /api/prints/{id}` | record a print (`{"note": "..."}`); delete a record |
| `GET /api/queue`, `PUT`/`DELETE /api/variants/{id}/queue` | the print queue; add / remove a variant |
| `PUT /api/models/{id}/hidden` | hide / show a model (`{"hidden": true}`) |
| `PUT`/`DELETE /api/variants/{id}/label` | correct a variant's dimensions and option (`{"dims": {...}, "option": "..."}`); back to the folder names |
| `GET /api/imports`, `POST /api/imports/request` | what the importer did with each download folder; import one (`{"source": "..."}`) |

Search takes `q`, `creator`, `tag`, `printed=yes|no`, `hidden=yes` (include hidden models), `limit`, `offset`.
Writes take `Content-Type: application/json` only.

There is no authentication: keep it on a private network (it listens on
localhost by default).

## Running with Docker

`deploy/combined/` holds a compose file for the image CI publishes:

```
cd deploy/combined
cp .env.example .env    # set REGISTRY_HOST, LIBRARY_PATH, …
docker compose up -d
```

The library is mounted read-only at `/library`; the index and thumbnail
cache live in the `data` volume. To import new downloads, add
`-f docker-compose.import.yml` (it mounts the library writable and the
downloads folder read-only at `/downloads`) and set `IMPORT_PATH`. The image is a single static binary on
distroless (about 24 MB), running as a non-root user.

## CI

`.github/workflows/ci.yml` vets and tests the Go code (with the race
detector), type-checks and builds the web UI, and builds the image. Pushes
to `main` publish `latest`, the commit SHA and `<sha>-snapshot`; a `vX.Y.Z`
tag also publishes `vX.Y.Z`, `vX.Y`, `vX`, `<version>-release` and cuts a
GitHub release. Publishing joins a Tailscale tailnet to reach a private
registry and needs:

| | |
|---|---|
| variable `REGISTRY_HOST` | the registry, e.g. `registry.example.internal:5000` |
| secrets `REGISTRY_USERNAME`, `REGISTRY_PASSWORD` | registry login |
| secret `REGISTRY_CA_CERT` | the registry's CA certificate (PEM), for a self-signed internal CA |
| secrets `TS_OAUTH_CLIENT_ID`, `TS_OAUTH_CLIENT_SECRET` | Tailscale OAuth client allowed to create `tag:ci` nodes |

## Building

```
(cd web && npm ci && npm run build)   # the UI, embedded into the binary
go test ./...
go build ./cmd/stlib
```

Without the UI build, `go build` still works; the server then says the UI
isn't included. For UI work, `npm run dev` in `web/` proxies `/api` to a
`stlib serve` on `127.0.0.1:8080`.
