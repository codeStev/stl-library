package app

import "context"

// StorageRelease is a release's share of a creator's space.
type StorageRelease struct {
	Name   string
	Models int
	Bytes  int64
}

// StorageCreator is what a creator's models take, release by release (biggest first).
type StorageCreator struct {
	Name     string
	Models   int
	Bytes    int64
	Releases []StorageRelease
}

// StorageModel is one of the biggest models.
type StorageModel struct {
	ID      int64
	Name    string
	Creator string
	Bytes   int64
}

// StorageKind is the space of one kind of file ("stl", "lys", "ctb" ...).
type StorageKind struct {
	Ext   string
	Files int
	Bytes int64
}

// StorageReport says where the library's space goes.
type StorageReport struct {
	Bytes           int64 // all files of all models
	Models          int
	Files           int
	Creators        []StorageCreator
	Largest         []StorageModel
	Kinds           []StorageKind
	DuplicateBytes  int64 // space the extra copies take (files already hashed)
	DuplicateGroups int
	Hashed, Total   int // how many files the duplicate figure is based on
}

// StorageReporter is implemented by stores that can report the library's storage.
type StorageReporter interface {
	Storage(ctx context.Context) (StorageReport, error)
}
