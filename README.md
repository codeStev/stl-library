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

Status: in development. What exists so far is the definition of the
convention (`convention/`).

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
  the variant, not a model of their own.

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
| app core, use cases + ports, adapters, driving adapters | added with the app (`internal/core`, `internal/app`, `internal/adapters`, `cmd/stlib`) | inward only |

## Building

```
go test ./...
```
