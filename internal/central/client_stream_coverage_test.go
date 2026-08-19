package central

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientServiceBootstrapUsesEventStreamCursor(t *testing.T) {
	repository := newClientStreamCoverageRepository(t)
	service := newClientStreamCoverageService(t, repository)
	principal := ClientPrincipal{UserID: "user"}

	repository.pageErr = errInjectedClient
	if _, err := service.Bootstrap(t.Context(), principal, ""); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Bootstrap(event page error) = %v", err)
	}

	repository.pageErr = nil
	repository.page = ClientEventPage{Latest: 42}
	bootstrap, err := service.Bootstrap(t.Context(), principal, "")
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.EventCursor != 42 {
		t.Fatalf("Bootstrap().EventCursor = %d, want 42", bootstrap.EventCursor)
	}
	if repository.pageUserID != "user" || repository.after != 0 || repository.limit != 1 {
		t.Fatalf(
			"ClientEventPage() arguments = (%q, %d, %d), want (%q, 0, 1)",
			repository.pageUserID,
			repository.after,
			repository.limit,
			"user",
		)
	}
}

func TestClientServiceEventPageStreamAndFallbackBoundaries(t *testing.T) {
	streamRepository := newClientStreamCoverageRepository(t)
	streamService := newClientStreamCoverageService(t, streamRepository)
	principal := ClientPrincipal{UserID: "user"}
	wantPage := ClientEventPage{
		Events:            []ClientEvent{{Sequence: 9, ID: "event"}},
		PrunedThrough:     3,
		Latest:            12,
		ReleaseGeneration: 4,
	}
	streamRepository.page = wantPage

	page, err := streamService.EventPage(t.Context(), principal, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Sequence != 9 || page.PrunedThrough != 3 ||
		page.Latest != 12 || page.ReleaseGeneration != 4 {
		t.Fatalf("EventPage() = %+v, want %+v", page, wantPage)
	}
	if streamRepository.pageUserID != "user" || streamRepository.after != 7 ||
		streamRepository.limit != DefaultEventPageSize {
		t.Fatalf(
			"ClientEventPage() arguments = (%q, %d, %d), want (%q, 7, %d)",
			streamRepository.pageUserID,
			streamRepository.after,
			streamRepository.limit,
			"user",
			DefaultEventPageSize,
		)
	}

	streamRepository.pageErr = errInjectedClient
	if _, err := streamService.EventPage(t.Context(), principal, 7, 1); !errors.Is(err, errInjectedClient) {
		t.Fatalf("EventPage(stream error) = %v", err)
	}

	_, baseRepository := newClientServiceHarness(t)
	fallbackRepository := &clientEventListCoverageRepository{
		clientRepositoryFake: baseRepository,
		err:                  errInjectedClient,
	}
	fallbackService, err := NewClientService(fallbackRepository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fallbackService.EventPage(t.Context(), principal, 8, 1); !errors.Is(err, errInjectedClient) {
		t.Fatalf("EventPage(fallback error) = %v", err)
	}

	fallbackRepository.err = nil
	page, err = fallbackService.EventPage(t.Context(), principal, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.Latest != 8 {
		t.Fatalf("EventPage(empty fallback) = %+v", page)
	}
}

func TestClientServiceWaitEventsBoundaries(t *testing.T) {
	repository := newClientStreamCoverageRepository(t)
	service := newClientStreamCoverageService(t, repository)
	principal := ClientPrincipal{UserID: "user"}

	if err := service.WaitEvents(t.Context(), principal, -1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WaitEvents(negative cursor) = %v", err)
	}
	if err := service.WaitEvents(t.Context(), principal, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WaitEvents(invalid generation) = %v", err)
	}

	repository.waitErr = errInjectedClient
	if err := service.WaitEvents(t.Context(), principal, 7, 4); !errors.Is(err, errInjectedClient) {
		t.Fatalf("WaitEvents(repository error) = %v", err)
	}
	if repository.waitUserID != "user" || repository.waitAfter != 7 || repository.waitGeneration != 4 {
		t.Fatalf(
			"WaitClientEvents() arguments = (%q, %d, %d), want (%q, 7, 4)",
			repository.waitUserID,
			repository.waitAfter,
			repository.waitGeneration,
			"user",
		)
	}

	repository.waitErr = nil
	if err := service.WaitEvents(t.Context(), principal, 7, 4); err != nil {
		t.Fatalf("WaitEvents() = %v", err)
	}

	fallbackService, _ := newClientServiceHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := fallbackService.WaitEvents(ctx, principal, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitEvents(cancelled fallback) = %v", err)
	}
}

type clientStreamCoverageRepository struct {
	*clientRepositoryFake
	page           ClientEventPage
	pageErr        error
	pageUserID     string
	waitErr        error
	waitUserID     string
	waitAfter      int64
	waitGeneration int64
}

func newClientStreamCoverageRepository(t *testing.T) *clientStreamCoverageRepository {
	t.Helper()
	_, repository := newClientServiceHarness(t)
	return &clientStreamCoverageRepository{clientRepositoryFake: repository}
}

func newClientStreamCoverageService(
	t *testing.T,
	repository *clientStreamCoverageRepository,
) *ClientService {
	t.Helper()
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (repository *clientStreamCoverageRepository) ClientEventPage(
	_ context.Context,
	userID string,
	after int64,
	limit int,
) (ClientEventPage, error) {
	repository.pageUserID = userID
	repository.after = after
	repository.limit = limit
	return repository.page, repository.pageErr
}

func (repository *clientStreamCoverageRepository) WaitClientEvents(
	_ context.Context,
	userID string,
	after int64,
	releaseGeneration int64,
) error {
	repository.waitUserID = userID
	repository.waitAfter = after
	repository.waitGeneration = releaseGeneration
	return repository.waitErr
}

type clientEventListCoverageRepository struct {
	*clientRepositoryFake
	err error
}

func (repository *clientEventListCoverageRepository) ListClientEvents(
	_ context.Context,
	_ string,
	after int64,
	limit int,
) ([]ClientEvent, error) {
	repository.after = after
	repository.limit = limit
	return nil, repository.err
}
