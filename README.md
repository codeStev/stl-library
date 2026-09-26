# stl-library

A lightweight, self-hosted manager for 3D-printing libraries (STL, Lychee,
Chitubox) that shows your collection as **Model → Variants → Parts**: a
model usually comes split into parts, and in several variants (scales,
supported/unsupported, hollow/solid, slicer formats).

Designed for small home servers: a single static Go binary, SQLite, low
memory, and low CPU/I/O priority for anything that touches the disk.

**The app never moves, renames or deletes your files.** It expects your
library to follow the folder convention below, reads conforming folders
exactly, and lists folders that don't conform so you can fix them yourself.

Status: in development. What exists so far: the definition of the
convention (`convention/`), `stlib check` (reads a library, reports its
models and every folder that doesn't follow the convention), and a SQLite
index with full-text search (`stlib scan`, `stlib search`).

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
| domain (pure) | `internal/core/library` - reads a file listing into models, variants, parts and issues | convention |
| use cases + ports | `internal/app` (`Check`, `Scan`, `Search`; ports `Lister`, `Store`) | core |
| adapters | `internal/adapters/disk` (read-only listing), `internal/adapters/sqlite` (index, FTS5) | app, core |
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

## Building

```
go test ./...
go build ./cmd/stlib
```
