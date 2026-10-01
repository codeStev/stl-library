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
- **Print on a network resin printer** (optional, ELEGOO SDCP printers such
  as the Saturn 4 Ultra): send a sliced `.ctb`/`.goo` part to the printer
  and start it, watch progress, pause/resume/stop, manage the files on the
  printer - from the web UI or with `stlib printer` on the command line.
- **Import from a downloads folder** (optional): new, fully downloaded
  models are copied into the library in the convention's layout, zip
  archives unpacked.
- **Notifications** (optional) via ntfy and/or email when a print finishes,
  is stopped or fails, and when imports are done or need attention. Prints
  started from the app are recorded in the print history when they finish.
- **Accounts**: sign-in with a mandatory second factor (authenticator app
  or passkey), recovery codes, signed-in devices, optional Google sign-in.
- `stlib check` / `stlib scan` / `stlib search` on the command line.

## Folder convention

```
<Creator>/<Release>/[<Category>]/<Model>/[<Scale>]/<Supports…>/[Hollow|Solid]/[<Option>/]<parts>
```

e.g. `Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported Lychee/`.
The full spec - optional levels, the exact spellings, categories, options -
lives in its own repo, **[stl-convention](https://github.com/codeStev/stl-convention)**,
together with the Go package this app parses folders with. Tools that
produce conforming folders (e.g. a migration script for an existing
library) can use that package without depending on the app.

## Architecture

Hexagonal. The dependency rule is enforced by `internal/architecture_test.go`:

| Layer | Packages | May depend on |
|---|---|---|
| domain (pure) | `internal/core/library` (reads a file listing into models, variants, parts and issues), `internal/core/thumb` (image thumbnails), `internal/core/render` (streaming STL rasterizer) | [stl-convention](https://github.com/codeStev/stl-convention) |
| use cases + ports | `internal/app` (`Check`, `Scan`, `Search`, `VariantZip`, `Thumbs`, `UserData`; ports `Lister`, `Store`, `Files`, `ThumbCache`) | core |
| adapters | `internal/adapters/sdcp` (ELEGOO printers; `sdcptest` is a mock printer), `internal/adapters/disk` (read-only listing and file access, thumbnail cache), `internal/adapters/sqlite` (index, FTS5, user data), `internal/adapters/httpapi` (JSON API, driving), `internal/adapters/web` (embedded UI) | app, core |
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
- **Archives:** zip archives are unpacked on import. `.7z` and `.rar`
  (also split volumes) are unpacked too where the `7z` and `unrar` programs
  are installed (found on the `PATH`, or `SEVENZIP_BIN` / `UNRAR_BIN`); the
  Docker image has neither, so there they stay plain files. An archive is
  unpacked once, as a whole, into `IMPORT_TMP` (default: the system's temp
  folder - it needs room for the largest archive); passworded or damaged
  archives wait and are reported on the Imports page.
- **Busts:** a bust shipped separately in a `Busts` folder (`<creator>/Busts/<model>`,
  or inside a release folder) is imported as the `Bust` scale of the model
  of that name, into the existing model folder; its render images go to the
  model. A `Busts` folder inside a download is the `Bust` scale too.
- **The library only gets new files** (written under a hidden temporary
  name, then renamed). A file of the same name with other content is kept,
  the new one is added as "… (imported)".
- **Moving instead of copying (`IMPORT_DELETE=true`):** once a download
  folder is imported, it is removed from the downloads, so the downloads
  folder only shows what hasn't been imported yet. Only after every file
  is verified byte for byte in the library (read back after copying, or
  already there with identical content); files that arrived after the
  import started, and the creator folder, stay. Without it, the downloads
  are only read. (The patreon-ingest downloader keeps track of what it has
  downloaded in its own database, so removed folders aren't downloaded
  again.)
- **The first run imports nothing:** it records what's already in the
  downloads folder (it may have been copied by hand before). Those
  folders can be imported one by one from the Imports page; everything
  that arrives later is imported automatically. When new files arrive in
  an imported folder, the missing ones are copied. Folders recorded as
  already there are never removed on their own: if you copied them into
  the library by hand, delete them from the downloads yourself.

`stlib import --db index.db --source <downloads> --dry-run <library>` shows
where every file would go without writing anything.

The downloads folder and the library must be separate folders (neither
inside the other): importing with `--delete` must never be able to delete
the library's own files. To unpack archives that already sit in the
library, move their folder out (a rename on the same disk), import it with
`--adopt --delete` and the library then has them unpacked in the
convention: `--adopt` imports what is in the downloads on the first run
instead of only recording it. `--merge` imports into a model folder that
already exists (files that are already there are skipped) instead of
making `<Model> (2)`: for archives that belong to a model whose other
files are unpacked already.

### Printing

Configure the printer on the Settings page (address, and the ports if they
differ from 3030/3000), or with `PRINTER_ADDR=<host>[:<port>]` as the
default. Then a `.ctb`/`.goo` part gets a **Print** button (upload & start,
or upload only), and the Printer page shows live status, the transfer, the
printer's files, and pause/resume/stop.

- Uploads run in the background, one at a time. They use 128 KiB chunks
  instead of the protocol's 1 MiB: a Saturn 4 Ultra reads requests larger
  than its socket buffer very slowly. Measured on one: 1 MiB chunks
  0.25 MB/s, 128 KiB 0.85 MB/s (a 206 MB file in 4 instead of 14.5
  minutes). What's left is the printer's own processing.
- After the last chunk the printer checks the file before it shows up
  (about 40 s for 206 MB); the app waits for that before starting a print.
  An interrupted upload would stay on the printer as `<id>_<name>`; the
  app removes it.
- `stlib printer send <file> --trace` shows where an upload's time goes -
  the network, the printer's receive window, the printer's processing -
  without starting a print; `--max-chunks N`, `--chunk-kb N` and
  `--no-check` are for experiments.
- Only sliced files print: `.chitubox` / `.lys` project files and STLs have
  to be sliced (and exported as `.ctb`/`.goo`) first. The printer refuses
  files sliced for another model or resolution - the app shows why.

The same without the web UI (the app's binary, nothing else to install):

```
stlib printer --host 192.168.1.50 status
stlib printer files [/local | / | /usb]
stlib printer send model.ctb [--print]
stlib printer send model.ctb --trace [--max-chunks N] [--chunk-kb N] [--no-check]
stlib printer print model.ctb
stlib printer rm model.ctb
stlib printer pause | resume | stop | watch
```

The printer is `--host`, else `PRINTER_ADDR`, else the address saved in the
app (`--db`, default `DATA_DIR/index.db`).

`sdcp-mock` (`go run ./cmd/sdcp-mock --http 127.0.0.1:13030 --udp
127.0.0.1:13000`) simulates a printer for trying all of this without one;
point the settings at it (or `--host 127.0.0.1:13030` with
`PRINTER_DISCOVERY_PORT=13000`). `--read-rate`, `--chunk-delay`, `--check-delay` and `--finalize-delay`
simulate a slow network or a slow printer for trying out `--trace`.

### Notifications

On the Settings page (admins): an [ntfy](https://ntfy.sh) topic URL (with
an optional access token) and/or an SMTP server, and which events to send -
print finished / stopped / error, import done / needs attention, someone
created an account. "Send
test" tries the entered values before saving. Tokens and passwords are
stored encrypted with the app secret (see below) and never sent back to the
browser.

### Accounts and sign-in

Everything but the sign-in pages needs an account. **The first account
becomes the admin** - so register yours right after the first start. After
that, anyone who can reach the app can register, unless
`OPEN_REGISTRATION=false`: then only admins add accounts (on the Accounts
page, with an initial password or as a Google address). Accounts share the
library, the print queue and the printer; admins also manage settings,
on-request imports and the accounts (add, roles, disable, reset a second
factor, set a password). The "Someone created an account" notification
tells admins about self-registrations.

- Passwords: at least 12 characters, bcrypt-hashed. Repeated failures lock
  the account for 15 minutes; sign-in, registration and recovery are rate
  limited per client. Answers never reveal whether an email has an account.
- A **second factor is mandatory**: an authenticator app (TOTP) or a
  passkey. Setting it up shows 10 single-use **recovery codes** - they sign
  you in when the second factor is lost (and you set up a new one), and
  reset a forgotten password (there is no email-based reset).
- The session is a signed token (24 h) in an HttpOnly, SameSite=Strict
  cookie; the account page lists signed-in devices and signs them out.
  Changing the password, a role or the second factor signs out other
  devices.
- **Passkeys** need `PUBLIC_URL` (the exact address users open, e.g.
  `https://stl.example.org`; browsers allow passkeys only over HTTPS or on
  `localhost`).
- **Google sign-in** (optional) needs `PUBLIC_URL`, `GOOGLE_CLIENT_ID` and
  `GOOGLE_CLIENT_SECRET` (an OAuth client of type "Web application" with the
  redirect URI `<PUBLIC_URL>/login/oauth2/code/google`). A Google account
  never takes over an existing password account with the same email, and it
  still needs the second factor. With registration closed, only Google
  addresses an admin added can sign in; the first sign-in binds the Google
  account to it.
- `APP_SECRET` (at least 32 characters) signs sessions and encrypts stored
  secrets. Without it, one is generated into `DATA_DIR/secret.key` - keep
  that file with the data (losing it signs everyone out and makes stored
  notification passwords unreadable).

### Server

```
LIBRARY_ROOT=/path/to/library DATA_DIR=./data stlib serve
```

| Variable | Default | |
|---|---|---|
| `LIBRARY_ROOT` | (required) | the library; only read, never written |
| `DATA_DIR` | `./data` | the index (`index.db`) |
| `LISTEN_ADDR` | `127.0.0.1:8080` | |
| `SCAN_INTERVAL` | `1h` | time between rescans (at least `1m`); the first scan starts right away. Admins can also start one on the Settings page ("Rescan now"); a finished import triggers one too. After each scan, cached thumbnails of files that moved or are gone are removed. |
| `IMPORT_SOURCE` | (off) | downloads folder to import from; the library must then be writable |
| `PRINTER_ADDR` | (off) | default printer address `<host>[:<control port>]`; the Settings page overrides it |
| `PRINTER_DISCOVERY_PORT` | `3000` | the printer's discovery port, if it differs |
| `IMPORT_INTERVAL` | `1h` | time between imports (at least `1m`) |
| `IMPORT_SETTLE` | `1h` | how long a download folder must be unchanged to count as complete |
| `IMPORT_TMP` | system temp | where 7z/rar archives are unpacked during an import |
| `IMPORT_DELETE` | `false` | `true`: remove imported folders from the downloads once verified in the library |
| `BACKUP_MARKER` | (off) | a file your backup job touches when it is done (or the backup folder); the Health page and the weekly summary show when the last backup was |
| `BACKUP_MAX_AGE` | `168h` | how old the backup marker may get before the backup counts as overdue |
| `APP_SECRET` | (generated) | at least 32 characters; else `DATA_DIR/secret.key` is created |
| `PUBLIC_URL` | (off) | the address users open the app at; turns on passkeys (and Google) |
| `OPEN_REGISTRATION` | `true` | `false`: after the first account, only admins add accounts |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | (off) | Google sign-in, with `PUBLIC_URL` |
| `TRUST_PROXY_HEADERS` | `false` | `true` behind a reverse proxy: client address from `X-Forwarded-For` (rate limits, device list) |

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
| `GET /api/printer` | printer status, current job and transfer |
| `POST /api/parts/{id}/print` | send a `.ctb`/`.goo` part to the printer (`{"start": true}` also starts it) |
| `POST /api/printer/{pause,resume,stop}`, `DELETE /api/printer/transfer` | control the print; cancel an upload |
| `GET /api/printer/files?dir=`, `POST /api/printer/files/print`, `POST /api/printer/files/delete` | the printer's storage |
| `GET`/`PUT /api/settings/printer`, `POST /api/settings/printer/test` | printer settings; test settings without saving |
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

## License

MIT - see [`LICENSE`](LICENSE).
