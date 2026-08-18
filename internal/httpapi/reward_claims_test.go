package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

type fakeRewardClaimService struct {
	result rewards.CreateClaimResult
	err    error
}

type recordingRewardClaimService struct {
	called bool
	cmd    rewards.CreateClaimCommand
	result rewards.CreateClaimResult
	err    error
}

func (s *recordingRewardClaimService) CreateClaim(_ context.Context, cmd rewards.CreateClaimCommand) (rewards.CreateClaimResult, error) {
	s.called = true
	s.cmd = cmd

	if s.err != nil {
		return rewards.CreateClaimResult{}, s.err
	}

	return s.result, nil
}

func (s fakeRewardClaimService) CreateClaim(_ context.Context, _ rewards.CreateClaimCommand) (rewards.CreateClaimResult, error) {
	if s.err != nil {
		return rewards.CreateClaimResult{}, s.err
	}

	return s.result, nil
}

func TestRewardClaimsHandlerRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, routeRewardClaims, nil)
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
	}
}

func TestRewardClaimsHandlerRequiresIdempotencyKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if !strings.Contains(rec.Body.String(), errorCodeIdempotencyKeyRequired) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeIdempotencyKeyRequired)
	}
}

func TestRewardClaimsHandlerRejectsInvalidIdempotencyKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		wantCode string
	}{
		{
			name:     "empty",
			key:      "",
			wantCode: errorCodeInvalidIdempotencyKey,
		},
		{
			name:     "whitespace only",
			key:      "   ",
			wantCode: errorCodeInvalidIdempotencyKey,
		},
		{
			name:     "too long",
			key:      strings.Repeat("a", maxIdempotencyKeyLength+1),
			wantCode: errorCodeInvalidIdempotencyKey,
		},
		{
			name:     "control character",
			key:      "claim-key\n123",
			wantCode: errorCodeInvalidIdempotencyKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingRewardClaimService{}
			req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
			req.Header.Set(headerIdempotencyKey, tt.key)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			testRewardClaimsHandler(service).ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}

			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), tt.wantCode)
			}

			if service.called {
				t.Fatal("service was called for invalid Idempotency-Key")
			}
		})
	}
}

func TestRewardClaimsHandlerRejectsMultipleIdempotencyKeys(t *testing.T) {
	service := &recordingRewardClaimService{}
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Add(headerIdempotencyKey, "claim-key-123")
	req.Header.Add(headerIdempotencyKey, "claim-key-456")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if !strings.Contains(rec.Body.String(), errorCodeInvalidIdempotencyKey) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeInvalidIdempotencyKey)
	}

	if service.called {
		t.Fatal("service was called for multiple Idempotency-Key values")
	}
}

func TestRewardClaimsHandlerRequiresJSONContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}

	if !strings.Contains(rec.Body.String(), errorCodeUnsupportedMediaType) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeUnsupportedMediaType)
	}
}

func TestRewardClaimsHandlerAcceptsJSONContentTypeWithCharset(t *testing.T) {
	service := &recordingRewardClaimService{
		result: rewards.CreateClaimResult{
			StatusCode:   http.StatusCreated,
			ResponseBody: []byte(`{}`),
		},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected service to be called")
	}
}

func TestRewardClaimsHandlerRejectsInvalidJSONBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "empty body",
			body:       "",
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidJSON,
		},
		{
			name:       "malformed json",
			body:       `{"player_id":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidJSON,
		},
		{
			name:       "unknown field",
			body:       `{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","extra":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "multiple json objects",
			body:       `{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"} {"another":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingRewardClaimService{}
			req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(tt.body))
			req.Header.Set(headerIdempotencyKey, "claim-key-123")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			testRewardClaimsHandler(service).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), tt.wantCode)
			}

			if service.called {
				t.Fatal("service was called for invalid JSON request body")
			}
		})
	}
}

func TestRewardClaimsHandlerMapsClaimValidation(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "required field",
			err:         rewards.ValidationError{Field: "player_id", Message: "player_id is required"},
			wantMessage: "player_id is required",
		},
		{
			name:        "invalid identifier",
			err:         rewards.ValidationError{Field: "reward_id", Message: "reward_id must not contain NUL characters"},
			wantMessage: "reward_id must not contain NUL characters",
		},
		{
			name:        "invalid idempotency key",
			err:         rewards.ValidationError{Field: "idempotency_key", Message: "idempotency key is invalid"},
			wantMessage: "idempotency key is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := fakeRewardClaimService{err: tt.err}
			req := httptest.NewRequest(
				http.MethodPost,
				routeRewardClaims,
				strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
			)
			req.Header.Set(headerIdempotencyKey, "claim-key-123")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			testRewardClaimsHandler(service).ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), errorCodeInvalidRequest) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeInvalidRequest)
			}
			if !strings.Contains(rec.Body.String(), tt.wantMessage) {
				t.Fatalf("response body = %q, want message %q", rec.Body.String(), tt.wantMessage)
			}
		})
	}
}

func TestRewardClaimsHandlerRejectsInvalidUTF8(t *testing.T) {
	body := append([]byte(`{"player_id":"player-`), 0xff)
	body = append(body, []byte(`","campaign_id":"campaign-123","reward_id":"reward-123"}`)...)
	service := &recordingRewardClaimService{}
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, bytes.NewReader(body))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), errorCodeInvalidJSON) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeInvalidJSON)
	}

	if service.called {
		t.Fatal("service was called for invalid UTF-8 request body")
	}
}

func TestRewardClaimsHandlerRejectsLargeBody(t *testing.T) {
	body := `{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"` +
		strings.Repeat("a", maxRewardClaimBodyBytes) +
		`"}`

	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(body))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}

	if !strings.Contains(rec.Body.String(), errorCodeRequestBodyTooLarge) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeRequestBodyTooLarge)
	}
}

func TestRewardClaimsHandlerRejectsLargeBodyAfterValidJSON(t *testing.T) {
	validBody := `{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`
	body := validBody + strings.Repeat(" ", maxRewardClaimBodyBytes)
	service := &recordingRewardClaimService{}

	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(body))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), errorCodeRequestBodyTooLarge) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeRequestBodyTooLarge)
	}

	if service.called {
		t.Fatal("expected oversized request not to reach the service")
	}
}

func TestRewardClaimsHandlerCreatesClaim(t *testing.T) {
	responseBody := `{"claim_id":"claim-123","player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","claimed_at":"2026-07-06T12:34:56.123456Z"}`

	service := &recordingRewardClaimService{
		result: rewards.CreateClaimResult{
			StatusCode:   http.StatusCreated,
			ResponseBody: []byte(responseBody),
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":" player-123 ","campaign_id":" campaign-123 ","reward_id":" reward-123 "}`),
	)
	req.Header.Set(headerIdempotencyKey, " claim-key-123 ")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected service to be called")
	}

	if service.cmd.PlayerID != " player-123 " {
		t.Fatalf("service player_id = %q, want %q", service.cmd.PlayerID, " player-123 ")
	}

	if service.cmd.CampaignID != " campaign-123 " {
		t.Fatalf("service campaign_id = %q, want %q", service.cmd.CampaignID, " campaign-123 ")
	}

	if service.cmd.RewardID != " reward-123 " {
		t.Fatalf("service reward_id = %q, want %q", service.cmd.RewardID, " reward-123 ")
	}

	if service.cmd.IdempotencyKey != "claim-key-123" {
		t.Fatalf("service idempotency key = %q, want %q", service.cmd.IdempotencyKey, "claim-key-123")
	}

	if rec.Body.String() != responseBody {
		t.Fatalf("response body = %s, want %s", rec.Body.String(), responseBody)
	}
}

func TestRewardClaimsHandlerWritesReplayHeader(t *testing.T) {
	responseBody := `{"error":{"code":"reward_already_claimed","message":"Reward has already been claimed"}}`

	service := &recordingRewardClaimService{
		result: rewards.CreateClaimResult{
			StatusCode:   http.StatusConflict,
			ResponseBody: []byte(responseBody),
			Replayed:     true,
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	if got := rec.Header().Get(headerIdempotentReplayed); got != "true" {
		t.Fatalf("%s = %q, want true", headerIdempotentReplayed, got)
	}

	if rec.Body.String() != responseBody {
		t.Fatalf("response body = %s, want %s", rec.Body.String(), responseBody)
	}
}

func TestRewardClaimsHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "idempotency key reused",
			err:        rewards.ErrIdempotencyKeyReused,
			wantStatus: http.StatusConflict,
			wantCode:   errorCodeIdempotencyKeyReused,
		},
		{
			name:       "service validation",
			err:        rewards.ValidationError{Field: "player_id", Message: "player_id is required"},
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "unavailable",
			err:        rewards.ErrUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   errorCodeUnavailable,
		},
		{
			name:       "internal",
			err:        rewards.ErrInternal,
			wantStatus: http.StatusInternalServerError,
			wantCode:   errorCodeInternal,
		},
		{
			name:       "context error without ended request context",
			err:        context.Canceled,
			wantStatus: http.StatusInternalServerError,
			wantCode:   errorCodeInternal,
		},
		{
			name:       "unknown",
			err:        errors.New("unexpected failure"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   errorCodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingRewardClaimService{
				err: tt.err,
			}

			req := httptest.NewRequest(
				http.MethodPost,
				routeRewardClaims,
				strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
			)
			req.Header.Set(headerIdempotencyKey, "claim-key-123")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			testRewardClaimsHandler(service).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if !service.called {
				t.Fatal("expected service to be called")
			}

			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), tt.wantCode)
			}
		})
	}
}

func TestRewardClaimsHandlerDoesNotWriteAfterRequestCancellation(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service := &recordingRewardClaimService{err: fmt.Errorf("create reward claim: %w", context.Canceled)}
	observer := &recordingRewardObserver{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	).WithContext(ctx)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(logger, service, observer).ServeHTTP(rec, req)

	if rec.Body.Len() != 0 {
		t.Fatalf("response body = %q, want no response body", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Fatalf("Content-Type = %q, want no response", got)
	}
	if !observer.called || !errors.Is(observer.err, context.Canceled) {
		t.Fatalf("unexpected observation: %#v", observer)
	}
	if strings.Contains(logs.String(), "reward claim request failed") {
		t.Fatalf("request cancellation was logged as a claim failure: %s", logs.String())
	}
}

func TestRewardClaimsHandlerDoesNotExposeServiceErrorDetails(t *testing.T) {
	const sensitiveDetail = "postgres://user:super-secret@internal-db.example:5432/game_rewards"

	service := &recordingRewardClaimService{
		err: fmt.Errorf("%s: %w", sensitiveDetail, rewards.ErrUnavailable),
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), sensitiveDetail) {
		t.Fatal("response exposed internal service error details")
	}

	if !strings.Contains(rec.Body.String(), errorCodeUnavailable) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeUnavailable)
	}
}

func TestRewardClaimsHandlerLogsSafeInternalError(t *testing.T) {
	const (
		requestID       = "request-123"
		sensitiveDetail = "postgres://user:super-secret@internal-db.example:5432/game_rewards"
	)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service := &recordingRewardClaimService{
		err: fmt.Errorf("%s: %w", sensitiveDetail, rewards.ErrInternal),
	}

	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req = req.WithContext(contextWithRequestID(req.Context(), requestID))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(logger, service, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	logOutput := logs.String()
	for _, want := range []string{
		`"msg":"reward claim request failed"`,
		`"request_id":"` + requestID + `"`,
		`"operation":"reward_claim_create"`,
		`"error_class":"internal"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("log output = %q, want %q", logOutput, want)
		}
	}

	if strings.Contains(logOutput, sensitiveDetail) {
		t.Fatal("log output exposed internal service error details")
	}
}

type recordingRewardObserver struct {
	called bool
	result rewards.CreateClaimResult
	err    error
}

func (o *recordingRewardObserver) ObserveRewardClaim(result rewards.CreateClaimResult, err error) {
	o.called = true
	o.result = result
	o.err = err
}

func TestRewardClaimsHandlerObservesServiceOutcome(t *testing.T) {
	service := &recordingRewardClaimService{
		result: rewards.CreateClaimResult{
			StatusCode:   http.StatusCreated,
			ResponseBody: []byte(`{"claim_id":"claim-1"}`),
		},
	}
	observer := &recordingRewardObserver{}

	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service, observer).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	if !observer.called || observer.result.StatusCode != http.StatusCreated || observer.err != nil {
		t.Fatalf("unexpected observation: %#v", observer)
	}
}

func TestRewardClaimsHandlerDoesNotObserveClaimValidation(t *testing.T) {
	observer := &recordingRewardObserver{}
	service := fakeRewardClaimService{
		err: rewards.ValidationError{Field: "player_id", Message: "player_id is required"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeRewardClaims,
		strings.NewReader(`{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`),
	)
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(service, observer).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	if observer.called {
		t.Fatal("claim validation must not be recorded as a reward claim operation")
	}
}

func TestRewardClaimsHandlerDoesNotObserveTransportValidation(t *testing.T) {
	observer := &recordingRewardObserver{}
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRewardClaimsHandler(fakeRewardClaimService{}, observer).ServeHTTP(rec, req)

	if observer.called {
		t.Fatal("transport validation must not be recorded as a service operation")
	}
}
