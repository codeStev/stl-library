package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	convention "github.com/codeStev/stl-convention"
)

type userStore struct {
	Store
	tags     []string
	name     string
	printed  int64
	dequeued bool
}

func (u *userStore) Tags(context.Context) ([]TagCount, error) {
	return []TagCount{{Tag: "Painted", Models: 3}}, nil
}
func (u *userStore) EditTags(_ context.Context, _ []int64, add, remove []string) error {
	u.tags = append(append([]string{}, add...), "-"+strings.Join(remove, ","))
	return nil
}
func (u *userStore) SetTags(_ context.Context, _ int64, t []string) error { u.tags = t; return nil }
func (u *userStore) SetDisplayName(_ context.Context, _ int64, n string) error {
	u.name = n
	return nil
}
func (u *userStore) AddPrint(_ context.Context, _ int64, at int64, note string) (Print, error) {
	u.printed = at
	return Print{ID: 1, AtUnix: at, Note: note}, nil
}
func (u *userStore) Dequeue(context.Context, int64) error { u.dequeued = true; return ErrNotFound }

func TestTagsTakeTheSpellingOfAnExistingTag(t *testing.T) {
	s := &userStore{}
	u := UserData{Store: s}
	ctx := context.Background()
	tags, err := u.SetTags(ctx, 1, []string{"painted", "new one"})
	if err != nil || strings.Join(tags, ",") != "Painted,new one" {
		t.Errorf("set: %v %v", tags, err)
	}
	added, removed, err := u.EditTags(ctx, []int64{1, 2}, []string{"PAINTED"}, []string{"painted"})
	if err != nil || strings.Join(added, ",") != "Painted" || strings.Join(removed, ",") != "Painted" {
		t.Errorf("edit: %v %v %v", added, removed, err)
	}
}

func TestUserDataNormalizesAndValidates(t *testing.T) {
	s := &userStore{}
	u := UserData{Store: s, Now: func() time.Time { return time.Unix(1000, 0) }}
	ctx := context.Background()
	tags, _ := u.SetTags(ctx, 1, []string{" a ", "A", "b"})
	if strings.Join(tags, ",") != "a,b" || strings.Join(s.tags, ",") != "a,b" {
		t.Errorf("tags %v / %v", tags, s.tags)
	}
	u.SetDisplayName(ctx, 1, "  Bell   Head ")
	if s.name != "Bell Head" {
		t.Errorf("name %q", s.name)
	}
	if err := u.SetDisplayName(ctx, 1, strings.Repeat("x", 201)); !errors.Is(err, ErrInvalid) {
		t.Errorf("long name: %v", err)
	}
	p, err := u.MarkPrinted(ctx, 1, " ok ")
	if err != nil || p.AtUnix != 1000 || p.Note != "ok" || !s.dequeued {
		t.Errorf("print %+v %v dequeued=%v", p, err, s.dequeued)
	}
	if _, err := u.MarkPrinted(ctx, 1, strings.Repeat("x", 501)); !errors.Is(err, ErrInvalid) {
		t.Errorf("long note: %v", err)
	}
}

type labelStore struct {
	Store
	label *VariantLabel
}

func (l *labelStore) SetVariantLabel(_ context.Context, _ int64, v *VariantLabel) error {
	l.label = v
	return nil
}

func TestRelabelAcceptsOnlyCanonicalValues(t *testing.T) {
	s := &labelStore{}
	u := UserData{Store: s}
	ctx := context.Background()
	ok := []VariantLabel{
		{Dims: convention.Dims{Scale: "32mm", Supports: "Supported", Format: "Lychee"}},
		{Dims: convention.Dims{Scale: "1-10", Split: "Combined"}},
		{Option: "  Helmet   Version "},
	}
	for _, l := range ok {
		if err := u.Relabel(ctx, 1, l); err != nil {
			t.Errorf("%+v: %v", l, err)
		}
	}
	if s.label.Option != "Helmet Version" {
		t.Errorf("option not normalized: %q", s.label.Option)
	}
	bad := []VariantLabel{
		{},
		{Dims: convention.Dims{Scale: "32 mm"}},
		{Dims: convention.Dims{Supports: "Presupported"}},
		{Dims: convention.Dims{Format: "lys"}},
		{Option: strings.Repeat("x", 101)},
	}
	for _, l := range bad {
		if err := u.Relabel(ctx, 1, l); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted", l)
		}
	}
	u.ResetLabel(ctx, 1)
	if s.label != nil {
		t.Error("reset")
	}
}
