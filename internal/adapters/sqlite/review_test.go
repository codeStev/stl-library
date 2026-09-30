package sqlite

import (
	"context"
	"testing"
)

func TestReviewQueueHoldsRenderedModelsNotYetReviewed(t *testing.T) {
	s := open(t)
	// Bell Head has a cover.jpg (a picture of its own); the others are rendered
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	list := func(creator string) []string {
		c, err := s.ReviewCandidates(ctx, creator, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range c {
			out = append(out, x.Dir)
		}
		return out
	}
	got := list("")
	want := []string{"Artisan Guild/Noble Alfar/Goldhorn Cervid Rider", "Loot Studios/Abyssal Haze/Heroes/Élise the Brave"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("queue: %v", got)
	}
	if rem, done, err := s.ReviewCounts(ctx, ""); err != nil || rem != 2 || done != 0 {
		t.Errorf("counts %d %d %v", rem, done, err)
	}
	if err := s.MarkReview(ctx, want[0], "skip", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReview(ctx, want[1], "set", 2); err != nil {
		t.Fatal(err)
	}
	if got := list(""); len(got) != 0 {
		t.Errorf("reviewed models came back: %v", got)
	}
	if n, err := s.ResetReviews(ctx, "Artisan Guild"); err != nil || n != 1 {
		t.Errorf("reset: %d %v", n, err)
	}
	if got := list("Artisan Guild"); len(got) != 1 {
		t.Errorf("after reset: %v", got)
	}
	if got := list("Loot Studios"); len(got) != 0 { // a chosen picture stays chosen
		t.Errorf("a set model was put back: %v", got)
	}
	if err := s.UnmarkReview(ctx, want[1]); err != nil {
		t.Fatal(err)
	}
	if got := list("Loot Studios"); len(got) != 1 {
		t.Errorf("undo: %v", got)
	}
}
