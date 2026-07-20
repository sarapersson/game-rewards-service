package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

type testCreateRewardClaimResponse struct {
	ClaimID    string `json:"claim_id"`
	PlayerID   string `json:"player_id"`
	CampaignID string `json:"campaign_id"`
	RewardID   string `json:"reward_id"`
	Status     string `json:"status"`
	ClaimedAt  string `json:"claimed_at"`
}

func TestRewardClaimsHandlerRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, routeRewardClaims, nil)
	rec := httptest.NewRecorder()

	rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
	}
}

func TestRewardClaimsHandlerRequiresService(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRewardClaimsHandlerRequiresIdempotencyKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

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

			rewardClaimsHandler(service).ServeHTTP(rec, req)

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

	rewardClaimsHandler(service).ServeHTTP(rec, req)

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

	rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}

	if !strings.Contains(rec.Body.String(), errorCodeUnsupportedMediaType) {
		t.Fatalf("response body = %q, want error code %q", rec.Body.String(), errorCodeUnsupportedMediaType)
	}
}

func TestRewardClaimsHandlerAcceptsJSONContentTypeWithCharset(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set(headerIdempotencyKey, "claim-key-123")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

	if rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, did not want unsupported media type", rec.Code)
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
		{
			name:       "missing player_id",
			body:       `{"campaign_id":"campaign-123","reward_id":"reward-123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "missing campaign_id",
			body:       `{"player_id":"player-123","reward_id":"reward-123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "missing reward_id",
			body:       `{"player_id":"player-123","campaign_id":"campaign-123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "player_id too long",
			body:       `{"player_id":"` + strings.Repeat("a", rewards.MaxIDLength+1) + `","campaign_id":"campaign-123","reward_id":"reward-123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "campaign_id too long",
			body:       `{"player_id":"player-123","campaign_id":"` + strings.Repeat("a", rewards.MaxIDLength+1) + `","reward_id":"reward-123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
		{
			name:       "reward_id too long",
			body:       `{"player_id":"player-123","campaign_id":"campaign-123","reward_id":"` + strings.Repeat("a", rewards.MaxIDLength+1) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errorCodeInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(tt.body))
			req.Header.Set(headerIdempotencyKey, "claim-key-123")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), tt.wantCode)
			}
		})
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

	rewardClaimsHandler(fakeRewardClaimService{}).ServeHTTP(rec, req)

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

	rewardClaimsHandler(service).ServeHTTP(rec, req)

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
	createdAt := time.Date(2026, 7, 6, 12, 34, 56, 123456000, time.UTC)

	service := &recordingRewardClaimService{
		result: rewards.CreateClaimResult{
			StatusCode: http.StatusCreated,
			ResponseBody: []byte(`{
				"claim_id":"claim-123",
				"player_id":"player-123",
				"campaign_id":"campaign-123",
				"reward_id":"reward-123",
				"status":"claimed",
				"claimed_at":"` + createdAt.Format(time.RFC3339Nano) + `"
			}`),
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

	rewardClaimsHandler(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected service to be called")
	}

	if service.cmd.PlayerID != "player-123" {
		t.Fatalf("service player_id = %q, want %q", service.cmd.PlayerID, "player-123")
	}

	if service.cmd.CampaignID != "campaign-123" {
		t.Fatalf("service campaign_id = %q, want %q", service.cmd.CampaignID, "campaign-123")
	}

	if service.cmd.RewardID != "reward-123" {
		t.Fatalf("service reward_id = %q, want %q", service.cmd.RewardID, "reward-123")
	}

	if service.cmd.IdempotencyKey != "claim-key-123" {
		t.Fatalf("service idempotency key = %q, want %q", service.cmd.IdempotencyKey, "claim-key-123")
	}

	var body testCreateRewardClaimResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.ClaimID != "claim-123" {
		t.Fatalf("response claim_id = %q, want %q", body.ClaimID, "claim-123")
	}

	if body.PlayerID != "player-123" {
		t.Fatalf("response player_id = %q, want %q", body.PlayerID, "player-123")
	}

	if body.CampaignID != "campaign-123" {
		t.Fatalf("response campaign_id = %q, want %q", body.CampaignID, "campaign-123")
	}

	if body.RewardID != "reward-123" {
		t.Fatalf("response reward_id = %q, want %q", body.RewardID, "reward-123")
	}

	if body.Status != rewards.ClaimStatusClaimed {
		t.Fatalf("response status = %q, want %q", body.Status, rewards.ClaimStatusClaimed)
	}

	if body.ClaimedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("response claimed_at = %q, want %q", body.ClaimedAt, createdAt.Format(time.RFC3339Nano))
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

	rewardClaimsHandler(service).ServeHTTP(rec, req)

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
		name           string
		err            error
		wantStatus     int
		wantCode       string
		wantRetryAfter string
	}{
		{
			name:       "duplicate claim",
			err:        rewards.ErrDuplicateClaim,
			wantStatus: http.StatusConflict,
			wantCode:   errorCodeRewardAlreadyClaimed,
		},
		{
			name:       "idempotency key reused",
			err:        rewards.ErrIdempotencyKeyReused,
			wantStatus: http.StatusConflict,
			wantCode:   errorCodeIdempotencyKeyReused,
		},
		{
			name:           "idempotency key in progress",
			err:            rewards.ErrIdempotencyInProgress,
			wantStatus:     http.StatusConflict,
			wantCode:       errorCodeIdempotencyInProgress,
			wantRetryAfter: "1",
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

			rewardClaimsHandler(service).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if !service.called {
				t.Fatal("expected service to be called")
			}

			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("response body = %q, want error code %q", rec.Body.String(), tt.wantCode)
			}

			if got := rec.Header().Get("Retry-After"); got != tt.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
		})
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

	rewardClaimsHandler(service).ServeHTTP(rec, req)

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

	rewardClaimsHandler(service, observer).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !observer.called || observer.result.StatusCode != http.StatusCreated || observer.err != nil {
		t.Fatalf("unexpected observation: %#v", observer)
	}
}

func TestRewardClaimsHandlerDoesNotObserveTransportValidation(t *testing.T) {
	observer := &recordingRewardObserver{}
	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rewardClaimsHandler(fakeRewardClaimService{}, observer).ServeHTTP(rec, req)

	if observer.called {
		t.Fatal("transport validation must not be recorded as a service operation")
	}
}
