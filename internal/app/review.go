package app

import (
	"context"
	"sync"
	"time"
)

// Review actions.
const (
	ReviewSet  = "set"  // a new picture was chosen
	ReviewSkip = "skip" // the current one is fine
)

// ReviewCandidate is a model up for the preview review.
type ReviewCandidate struct {
	ID  int64
	Dir string
}

// ReviewStore keeps which models the preview review has been through.
type ReviewStore interface {
	ReviewCandidates(ctx context.Context, creator string, skip, limit int) ([]ReviewCandidate, error)
	ReviewCounts(ctx context.Context, creator string) (remaining, done int, err error)
	MarkReview(ctx context.Context, dir, action string, atUnix int64) error
	UnmarkReview(ctx context.Context, dir string) error
	ResetReviews(ctx context.Context, creator string) (int, error)
}

// ReviewState is what is left of the review.
type ReviewState struct {
	Remaining, Done int
	Next            []int64 // the next models, in the order to show them
}

// PreviewReview walks through the models to correct their preview pictures, one after the other. A model
// that was reviewed - a picture chosen or skipped - does not come up again. Models that already have a
// chosen picture count as reviewed.
type PreviewReview struct {
	Store     Store
	Reviews   ReviewStore
	Overrides PreviewOverrides
	Now       func() time.Time

	mu         sync.Mutex
	backfilled bool
}

func (r *PreviewReview) now() int64 {
	if r.Now != nil {
		return r.Now().Unix()
	}
	return time.Now().Unix()
}

// backfill marks the models that got a chosen picture before the review existed.
func (r *PreviewReview) backfill(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backfilled || r.Overrides == nil {
		return nil
	}
	for skip := 0; ; {
		batch, err := r.Reviews.ReviewCandidates(ctx, "", skip, 500)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		marked := 0
		for _, c := range batch {
			if at, ok := r.Overrides.Stat(overrideKey(c.Dir)); ok {
				if err := r.Reviews.MarkReview(ctx, c.Dir, ReviewSet, at); err != nil {
					return err
				}
				marked++
			}
		}
		skip += len(batch) - marked // the marked ones left the list
	}
	r.backfilled = true
	return nil
}

// State returns the counts and the next few models (limit 1 to 10) of a creator's models ("" for all).
func (r *PreviewReview) State(ctx context.Context, creator string, limit int) (ReviewState, error) {
	var st ReviewState
	if limit < 1 || limit > 10 {
		limit = 3
	}
	if err := r.backfill(ctx); err != nil {
		return st, err
	}
	var err error
	if st.Remaining, st.Done, err = r.Reviews.ReviewCounts(ctx, creator); err != nil {
		return st, err
	}
	cands, err := r.Reviews.ReviewCandidates(ctx, creator, 0, limit)
	if err != nil {
		return st, err
	}
	st.Next = make([]int64, 0, len(cands))
	for _, c := range cands {
		st.Next = append(st.Next, c.ID)
	}
	return st, nil
}

// Skip says the current picture of a model is fine.
func (r *PreviewReview) Skip(ctx context.Context, modelID int64) error {
	m, err := r.Store.Model(ctx, modelID)
	if err != nil {
		return err
	}
	return r.Reviews.MarkReview(ctx, m.Dir, ReviewSkip, r.now())
}

// Undo takes a model back into the review (the step back).
func (r *PreviewReview) Undo(ctx context.Context, modelID int64) error {
	m, err := r.Store.Model(ctx, modelID)
	if err != nil {
		return err
	}
	return r.Reviews.UnmarkReview(ctx, m.Dir)
}

// Reset puts the skipped models (of a creator, or all) back into the review; chosen pictures stay.
func (r *PreviewReview) Reset(ctx context.Context, creator string) (int, error) {
	return r.Reviews.ResetReviews(ctx, creator)
}
