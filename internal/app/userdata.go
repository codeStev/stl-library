package app

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codeStev/stl-library/convention"
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

// SetHidden hides a model from the library, or shows it again.
func (u UserData) SetHidden(ctx context.Context, modelID int64, hidden bool) error {
	return u.Store.SetHidden(ctx, modelID, hidden)
}

// Relabel overrides what a variant's folders say about it. Every value
// must be a canonical one (see package convention); an empty label is
// refused - use ResetLabel.
func (u UserData) Relabel(ctx context.Context, variantID int64, l VariantLabel) error {
	l.Option = strings.Join(strings.Fields(l.Option), " ")
	if !validLabel(l) {
		return ErrInvalid
	}
	return u.Store.SetVariantLabel(ctx, variantID, &l)
}

// ResetLabel goes back to what the folders say.
func (u UserData) ResetLabel(ctx context.Context, variantID int64) error {
	return u.Store.SetVariantLabel(ctx, variantID, nil)
}

func validLabel(l VariantLabel) bool {
	d := l.Dims
	if !d.Any() && l.Option == "" || utf8.RuneCountInString(l.Option) > 100 {
		return false
	}
	in := func(v string, list []string) bool {
		if v == "" {
			return true
		}
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	if d.Scale != "" {
		if pd, ok := convention.ParseSegment(d.Scale); !ok || pd.Scale != d.Scale {
			return false
		}
	}
	return in(d.Supports, convention.Supports) && in(d.Density, convention.Densities) && in(d.Format, convention.Formats) &&
		in(d.Fill, convention.Fills) && in(d.Split, []string{"Parts", "Combined"}) && in(d.Tech, convention.Techs) &&
		in(d.Extra, convention.Extras)
}
