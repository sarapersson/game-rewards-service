package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

const (
	routeRewardClaims = "/v1/reward-claims"

	headerIdempotencyKey = "Idempotency-Key"

	maxIdempotencyKeyLength = 255
	maxRewardClaimBodyBytes = 64 * 1024

	errorCodeRewardAlreadyClaimed   = "reward_already_claimed"
	errorCodeInvalidIdempotencyKey  = "invalid_idempotency_key"
	errorCodeIdempotencyKeyRequired = "idempotency_key_required"
	errorCodeInvalidJSON            = "invalid_json"
	errorCodeInvalidRequest         = "invalid_request"
	errorCodeRequestBodyTooLarge    = "request_body_too_large"
	errorCodeUnsupportedMediaType   = "unsupported_media_type"
	errorCodeUnavailable            = "service_unavailable"
)

type rewardClaimCreator interface {
	CreateClaim(ctx context.Context, cmd rewards.CreateClaimCommand) (rewards.Claim, error)
}

type createRewardClaimRequest struct {
	PlayerID   string `json:"player_id"`
	CampaignID string `json:"campaign_id"`
	RewardID   string `json:"reward_id"`
}

type createRewardClaimResponse struct {
	ClaimID    string `json:"claim_id"`
	PlayerID   string `json:"player_id"`
	CampaignID string `json:"campaign_id"`
	RewardID   string `json:"reward_id"`
	Status     string `json:"status"`
	ClaimedAt  string `json:"claimed_at"`
}

func rewardClaimsHandler(service rewardClaimCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}

		if service == nil {
			writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "Service unavailable")
			return
		}

		if err := validateIdempotencyKey(r.Header.Get(headerIdempotencyKey)); err != nil {
			if err == errMissingIdempotencyKey {
				writeError(w, http.StatusBadRequest, errorCodeIdempotencyKeyRequired, "Idempotency-Key header is required")
				return
			}

			writeError(w, http.StatusBadRequest, errorCodeInvalidIdempotencyKey, "Idempotency-Key header is invalid")
			return
		}

		if !isJSONContentType(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusUnsupportedMediaType, errorCodeUnsupportedMediaType, "Content-Type must be application/json")
			return
		}

		req, ok := decodeCreateRewardClaimRequest(w, r)
		if !ok {
			return
		}

		if err := validateCreateRewardClaimRequest(req); err != nil {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
			return
		}

		claim, err := service.CreateClaim(r.Context(), rewards.CreateClaimCommand{
			PlayerID:   strings.TrimSpace(req.PlayerID),
			CampaignID: strings.TrimSpace(req.CampaignID),
			RewardID:   strings.TrimSpace(req.RewardID),
		})
		if err != nil {
			writeCreateClaimError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, newCreateRewardClaimResponse(claim))
	}
}

var errMissingIdempotencyKey = rewardClaimValidationError("missing idempotency key")

type rewardClaimValidationError string

func (e rewardClaimValidationError) Error() string {
	return string(e)
}

func validateIdempotencyKey(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errMissingIdempotencyKey
	}

	if len(value) > maxIdempotencyKeyLength {
		return rewardClaimValidationError("idempotency key too long")
	}

	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return rewardClaimValidationError("idempotency key contains control character")
		}
	}

	return nil
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}

	return mediaType == "application/json"
}

func decodeCreateRewardClaimRequest(w http.ResponseWriter, r *http.Request) (createRewardClaimRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRewardClaimBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var req createRewardClaimRequest
	if err := decoder.Decode(&req); err != nil {
		writeDecodeError(w, err)
		return createRewardClaimRequest{}, false
	}

	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, errorCodeInvalidJSON, "Request body must contain a single JSON object")
		return createRewardClaimRequest{}, false
	}

	return req, true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, errorCodeRequestBodyTooLarge, "Request body is too large")
		return
	}

	if errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, errorCodeInvalidJSON, "Request body is required")
		return
	}

	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "Request body contains an unknown field")
		return
	}

	writeError(w, http.StatusBadRequest, errorCodeInvalidJSON, "Request body must be valid JSON")
}

func validateCreateRewardClaimRequest(req createRewardClaimRequest) error {
	playerID := strings.TrimSpace(req.PlayerID)
	if playerID == "" {
		return fmt.Errorf("player_id is required")
	}

	if utf8.RuneCountInString(playerID) > rewards.MaxIDLength {
		return fmt.Errorf("player_id must be at most 128 characters")
	}

	campaignID := strings.TrimSpace(req.CampaignID)
	if campaignID == "" {
		return fmt.Errorf("campaign_id is required")
	}

	if utf8.RuneCountInString(campaignID) > rewards.MaxIDLength {
		return fmt.Errorf("campaign_id must be at most 128 characters")
	}

	rewardID := strings.TrimSpace(req.RewardID)
	if rewardID == "" {
		return fmt.Errorf("reward_id is required")
	}

	if utf8.RuneCountInString(rewardID) > rewards.MaxIDLength {
		return fmt.Errorf("reward_id must be at most 128 characters")
	}

	return nil
}

func newCreateRewardClaimResponse(claim rewards.Claim) createRewardClaimResponse {
	return createRewardClaimResponse{
		ClaimID:    claim.ID,
		PlayerID:   claim.PlayerID,
		CampaignID: claim.CampaignID,
		RewardID:   claim.RewardID,
		Status:     claim.Status,
		ClaimedAt:  claim.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeCreateClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rewards.ErrDuplicateClaim):
		writeError(w, http.StatusConflict, errorCodeRewardAlreadyClaimed, "Reward has already been claimed")
	case rewards.IsValidationError(err):
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
	case errors.Is(err, rewards.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "Service unavailable")
	case errors.Is(err, rewards.ErrInternal):
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "Internal server error")
	default:
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "Internal server error")
	}
}
