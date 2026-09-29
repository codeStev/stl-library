package app

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// Collection is a named set of models the user put together (models for
// a scenario, a project). The folders are never touched.
type Collection struct {
	ID     int64
	Name   string
	Note   string
	Models int
}

const maxBulkCollection = 5000

// Collections manages the user's collections.
type Collections struct {
	Store Store
	Now   func() time.Time
}

func (c Collections) now() int64 {
	if c.Now != nil {
		return c.Now().Unix()
	}
	return time.Now().Unix()
}

func cleanCollection(name, note string) (string, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	note = strings.TrimSpace(note)
	if name == "" || utf8.RuneCountInString(name) > 100 || utf8.RuneCountInString(note) > maxText {
		return "", "", ErrInvalid
	}
	return name, note, nil
}

// List returns all collections with their number of models.
func (c Collections) List(ctx context.Context) ([]Collection, error) { return c.Store.Collections(ctx) }

// Create makes a collection; a name already in use (in any case) is ErrExists.
func (c Collections) Create(ctx context.Context, name, note string) (Collection, error) {
	name, note, err := cleanCollection(name, note)
	if err != nil {
		return Collection{}, err
	}
	return c.Store.CreateCollection(ctx, name, note, c.now())
}

// Update renames a collection or changes its note.
func (c Collections) Update(ctx context.Context, id int64, name, note string) error {
	name, note, err := cleanCollection(name, note)
	if err != nil {
		return err
	}
	return c.Store.UpdateCollection(ctx, id, name, note)
}

// Delete removes a collection (not the models).
func (c Collections) Delete(ctx context.Context, id int64) error {
	return c.Store.DeleteCollection(ctx, id)
}

// Edit adds and removes models.
func (c Collections) Edit(ctx context.Context, id int64, add, remove []int64) error {
	if len(add)+len(remove) == 0 || len(add) > maxBulkCollection || len(remove) > maxBulkCollection {
		return ErrInvalid
	}
	return c.Store.EditCollection(ctx, id, add, remove, c.now())
}

// OfModel returns the collections a model is in.
func (c Collections) OfModel(ctx context.Context, modelID int64) ([]Collection, error) {
	return c.Store.ModelCollections(ctx, modelID)
}
