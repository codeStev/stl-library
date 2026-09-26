// Package library is the app's domain: a library read from a folder tree
// that follows the convention (see package convention), as creators,
// releases, models, variants and parts. It is pure - it works on a listing
// of files, never on the disk.
package library

import "github.com/codeStev/stl-library/convention"

// File is one file of the listing, with its path relative to the library
// root in slash form ("Loot Studios/Abyssal Haze/Bell Head/32mm/Supported/a.stl").
type File struct {
	Path    string
	Size    int64
	ModUnix int64
}

// Model is one printable model: its identity from the folder levels above
// it, and the variants it comes in.
type Model struct {
	Creator  string
	Release  string // "" for a model directly under its creator
	Category string // "" when the release has no category level
	Name     string
	Dir      string // the model folder, relative to the library root
	Variants []*Variant
	Images   []File // images and documents in the model folder or below
}

// Variant is one combination of dimensions (and optionally an option, an
// alternative inside the model) with the part files printed for it.
type Variant struct {
	Dims   convention.Dims
	Option string
	Dir    string
	Parts  []File
}

// Issue is a folder that doesn't follow the convention. The app lists it
// and leaves it alone; fixing it is up to the user.
type Issue struct {
	Dir    string
	Reason string
}
