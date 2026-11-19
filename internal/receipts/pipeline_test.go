package receipts

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// pipelineFixture is one user with receipts in every stage.
type pipelineFixture struct {
	q       *sqlc.Queries
	service *Service
	alice   uuid.UUID
	bob     uuid.UUID
	now     time.Time
}

func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := func(email string) uuid.UUID {
		t.Helper()
		u, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: email})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	return &pipelineFixture{
		q: q, service: New(pool, func() time.Time { return now }),
		alice: user("pipeline-alice@example.com"), bob: user("pipeline-bob@example.com"),
		now: now,
	}
}

// receipt inserts one receipt in the given status, created at the given
// time, and returns its id. An id below another id of the same timestamp
// sorts first, which is what the tie-break expectations rely on.
func (f *pipelineFixture) receipt(t *testing.T, user uuid.UUID, status string, id uuid.UUID, at time.Time) uuid.UUID {
	t.Helper()
	if err := f.q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{
		ID: id, UserID: user, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: at,
	}); err != nil {
		t.Fatal(err)
	}
	switch status {
	case "queued":
		return id
	case "failed":
		reason := "The extraction service could not read this image."
		if err := f.q.FailExtraction(t.Context(), sqlc.FailExtractionParams{
			FailureReason: &reason, Now: f.now, UserID: user, ReceiptID: id,
		}); err != nil {
			t.Fatal(err)
		}
	default:
		if err := f.q.SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{
			Status: status, Now: f.now, UserID: user, ReceiptID: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (f *pipelineFixture) stage(t *testing.T, user uuid.UUID, id uuid.UUID) PipelineItem {
	t.Helper()
	items, err := f.service.Pipeline(t.Context(), user, []uuid.UUID{id})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("asked about one receipt, got %d items", len(items))
	}
	return items[0]
}

func TestPipeline_QueuePositionsCountThisUsersReceiptsInUploadOrder(t *testing.T) {
	f := newPipelineFixture(t)
	first := f.receipt(t, f.alice, "queued", uuid.MustParse("0193a1b2-0000-7000-8000-000000000001"), f.now)
	second := f.receipt(t, f.alice, "queued", uuid.MustParse("0193a1b2-0000-7000-8000-000000000002"), f.now)
	later := f.receipt(t, f.alice, "queued", uuid.MustParse("0193a1b2-0000-7000-8000-000000000003"), f.now.Add(time.Minute))
	// Bob's queue is invisible to Alice, in the count and in the rows.
	f.receipt(t, f.bob, "queued", uuid.MustParse("0193a1b2-0000-7000-8000-000000000004"), f.now)

	want := map[uuid.UUID]PipelineStage{
		first: PipelineStageQueued, second: PipelineStageQueued, later: PipelineStageQueued,
	}
	got := map[uuid.UUID]int64{}
	items, err := f.service.Pipeline(t.Context(), f.alice, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	for _, item := range items {
		if item.Stage != want[item.ReceiptID] {
			t.Errorf("receipt %s stage = %q, want %q", item.ReceiptID, item.Stage, want[item.ReceiptID])
		}
		got[item.ReceiptID] = item.QueuePosition
	}
	if diff := cmp.Diff(map[uuid.UUID]int64{first: 1, second: 2, later: 3}, got); diff != "" {
		t.Errorf("queue positions mismatch (-want +got):\n%s", diff)
	}
	// The same order serves the rows without ids.
	if diff := cmp.Diff([]uuid.UUID{first, second, later}, []uuid.UUID{items[0].ReceiptID, items[1].ReceiptID, items[2].ReceiptID}); diff != "" {
		t.Errorf("row order mismatch (-want +got):\n%s", diff)
	}
}

func TestPipeline_ProcessingSplitsIntoExtractingAndMatching(t *testing.T) {
	f := newPipelineFixture(t)
	extracting := f.receipt(t, f.alice, "processing", uuid.New(), f.now)
	matching := f.receipt(t, f.alice, "processing", uuid.New(), f.now.Add(time.Second))
	if err := f.q.CreateExtraction(t.Context(), sqlc.CreateExtractionParams{
		UserID: f.alice, ReceiptID: matching, Provider: "fake", Model: "synthetic", Raw: []byte("{}"), Now: f.now,
	}); err != nil {
		t.Fatal(err)
	}

	if got, want := f.stage(t, f.alice, extracting).Stage, PipelineStageExtracting; got != want {
		t.Errorf("receipt without a saved raw stage = %q, want %q", got, want)
	}
	if got, want := f.stage(t, f.alice, matching).Stage, PipelineStageMatching; got != want {
		t.Errorf("receipt with a saved raw stage = %q, want %q", got, want)
	}
}

func TestPipeline_SettledReceiptsCarryTheirStageAndReason(t *testing.T) {
	f := newPipelineFixture(t)
	ready := f.receipt(t, f.alice, "ready", uuid.New(), f.now)
	review := f.receipt(t, f.alice, "needs_review", uuid.New(), f.now.Add(time.Second))
	quota := f.receipt(t, f.alice, "waiting_quota", uuid.New(), f.now.Add(2*time.Second))
	failed := f.receipt(t, f.alice, "failed", uuid.New(), f.now.Add(3*time.Second))

	for id, want := range map[uuid.UUID]PipelineStage{
		ready: PipelineStageReady, review: PipelineStageNeedsReview,
		quota: PipelineStageWaitingQuota, failed: PipelineStageFailed,
	} {
		if got := f.stage(t, f.alice, id).Stage; got != want {
			t.Errorf("receipt %s stage = %q, want %q", id, got, want)
		}
	}
	reason := f.stage(t, f.alice, failed).FailureReason
	if reason == nil || *reason != "The extraction service could not read this image." {
		t.Errorf("failed receipt reason = %v, want the stored reason", reason)
	}
	// Without ids only the unsettled receipts appear, in upload order.
	items, err := f.service.Pipeline(t.Context(), f.alice, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if diff := cmp.Diff([]uuid.UUID{quota}, idsOf(items)); diff != "" {
		t.Errorf("unsettled receipts mismatch (-want +got):\n%s", diff)
	}
}

func TestPipeline_IsolatesUsers(t *testing.T) {
	f := newPipelineFixture(t)
	mine := f.receipt(t, f.alice, "queued", uuid.New(), f.now)
	theirs := f.receipt(t, f.bob, "queued", uuid.New(), f.now)

	items, err := f.service.Pipeline(t.Context(), f.bob, []uuid.UUID{mine, theirs})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if diff := cmp.Diff([]uuid.UUID{theirs}, idsOf(items)); diff != "" {
		t.Errorf("Bob asking about Alice's receipt mismatch (-want +got):\n%s", diff)
	}
}

func TestPipeline_RejectsMoreThanAHundredIds(t *testing.T) {
	f := newPipelineFixture(t)
	ids := make([]uuid.UUID, 101)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := f.service.Pipeline(t.Context(), f.alice, ids); !isInvalid(err) {
		t.Fatalf("pipeline with 101 ids = %v, want invalid", err)
	}
}

func isInvalid(err error) bool {
	var classified *apperr.Error
	return errors.As(err, &classified) && classified.Kind == apperr.Invalid
}

func idsOf(items []PipelineItem) []uuid.UUID {
	ids := make([]uuid.UUID, len(items))
	for i, item := range items {
		ids[i] = item.ReceiptID
	}
	return ids
}
