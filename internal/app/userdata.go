package app

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codeStev/stl-library/internal/core/library"
)

// ErrInvalid is returned for input that can't be stored.
var ErrInvalid = errors.New("invalid input")

// UserData changes what the user attaches to models: tags, a display
// name, print records and the print queue. The folder structure is never
// changed.
type UserData struct {
	Store Store
	Now   func() time.Time
}

const maxText = 500

func (u UserData) now() int64 {
	if u.Now != nil {
		return u.Now().Unix()
	}
	return time.Now().Unix()
}

// SetTags replaces a model's tags (normalized, see library.NormalizeTags).
func (u UserData) SetTags(ctx context.Context, modelID int64, tags []string) ([]string, error) {
	tags = library.NormalizeTags(tags)
	return tags, u.Store.SetTags(ctx, modelID, tags)
}

// SetDisplayName sets the name shown instead of the folder name; an empty
// name goes back to the folder name.
func (u UserData) SetDisplayName(ctx context.Context, modelID int64, name string) error {
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > 200 {
		return ErrInvalid
	}
	return u.Store.SetDisplayName(ctx, modelID, name)
}

// MarkPrinted records a print of a variant now, and takes it off the queue.
func (u UserData) MarkPrinted(ctx context.Context, variantID int64, note string) (Print, error) {
	if utf8.RuneCountInString(note) > maxText {
		return Print{}, ErrInvalid
	}
	p, err := u.Store.AddPrint(ctx, variantID, u.now(), strings.TrimSpace(note))
	if err != nil {
		return Print{}, err
	}
	if err := u.Store.Dequeue(ctx, variantID); err != nil && !errors.Is(err, ErrNotFound) {
		return p, err
	}
	return p, nil
}

// Enqueue puts a variant on the print queue (again: it keeps its place).
func (u UserData) Enqueue(ctx context.Context, variantID int64, note string) error {
	if utf8.RuneCountInString(note) > maxText {
		return ErrInvalid
	}
	return u.Store.Enqueue(ctx, variantID, u.now(), strings.TrimSpace(note))
}
