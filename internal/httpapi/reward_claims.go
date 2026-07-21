package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

const (
	routeRewardClaims = "/v1/reward-claims"

	headerIdempotencyKey     = "Idempotency-Key"
	headerIdempotentReplayed = "Idempotent-Replayed"

	maxIdempotencyKeyLength = 255
	maxRewardClaimBodyBytes = 64 * 1024

	errorCodeRewardAlreadyClaimed   = "reward_already_claimed"
	errorCodeInvalidIdempotencyKey  = "invalid_idempotency_key"
	errorCodeIdempotencyKeyRequired = "idempotency_key_required"
	errorCodeIdempotencyKeyReused   = "idempotency_key_reused"
	errorCodeIdempotencyInProgress  = "idempotency_key_in_progress"
	errorCodeInvalidJSON            = "invalid_json"
	errorCodeInvalidRequest         = "invalid_request"
	errorCodeRequestBodyTooLarge    = "request_body_too_large"
	errorCodeUnsupportedMediaType   = "unsupported_media_type"
	errorCodeUnavailable            = "service_unavailable"
)

type rewardClaimCreator interface {
	CreateClaim(ctx context.Context, cmd rewards.CreateClaimCommand) (rewards.CreateClaimResult, error)
}

type createRewardClaimRequest struct {
	PlayerID   string `json:"player_id"`
	CampaignID string `json:"campaign_id"`
	RewardID   string `json:"reward_id"`
}

func rewardClaimsHandler(service rewardClaimCreator, observers ...RewardClaimObserver) http.HandlerFunc {
	return rewardClaimsHandlerWithLogger(nil, service, observers...)
}

func rewardClaimsHandlerWithLogger(
	logger *slog.Logger,
	service rewardClaimCreator,
	observers ...RewardClaimObserver,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}

		if service == nil {
			writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "Service unavailable")
			return
		}

		idempotencyKey, err := readIdempotencyKey(r.Header)
		if err != nil {
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

		result, err := service.CreateClaim(r.Context(), rewards.CreateClaimCommand{
			PlayerID:       strings.TrimSpace(req.PlayerID),
			CampaignID:     strings.TrimSpace(req.CampaignID),
			RewardID:       strings.TrimSpace(req.RewardID),
			IdempotencyKey: idempotencyKey,
		})
		for _, observer := range observers {
			if observer != nil {
				observer.ObserveRewardClaim(result, err)
			}
		}
		if err != nil {
			logCreateClaimError(r, logger, err)
			writeCreateClaimError(w, err)
			return
		}

		if result.Replayed {
			w.Header().Set(headerIdempotentReplayed, "true")
		}

		writeRawJSON(w, result.StatusCode, result.ResponseBody)
	}
}

var errMissingIdempotencyKey = rewardClaimValidationError("missing idempotency key")

type rewardClaimValidationError string

func (e rewardClaimValidationError) Error() string {
	return string(e)
}

func readIdempotencyKey(header http.Header) (string, error) {
	values := header.Values(headerIdempotencyKey)
	if len(values) == 0 {
		return "", errMissingIdempotencyKey
	}

	if len(values) != 1 {
		return "", rewardClaimValidationError("multiple idempotency keys")
	}

	value := strings.TrimSpace(values[0])
	if err := validateIdempotencyKey(value); err != nil {
		return "", err
	}

	return value, nil
}

func validateIdempotencyKey(value string) error {
	if value == "" {
		return rewardClaimValidationError("idempotency key is empty")
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

	var extra json.RawMessage
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return req, true
	case err == nil:
		writeError(w, http.StatusBadRequest, errorCodeInvalidJSON, "Request body must contain a single JSON object")
		return createRewardClaimRequest{}, false
	default:
		writeDecodeError(w, err)
		return createRewardClaimRequest{}, false
	}
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

	if strings.ContainsRune(playerID, '\x00') {
		return fmt.Errorf("player_id must not contain NUL characters")
	}

	if utf8.RuneCountInString(playerID) > rewards.MaxIDLength {
		return fmt.Errorf("player_id must be at most %d characters", rewards.MaxIDLength)
	}

	campaignID := strings.TrimSpace(req.CampaignID)
	if campaignID == "" {
		return fmt.Errorf("campaign_id is required")
	}

	if strings.ContainsRune(campaignID, '\x00') {
		return fmt.Errorf("campaign_id must not contain NUL characters")
	}

	if utf8.RuneCountInString(campaignID) > rewards.MaxIDLength {
		return fmt.Errorf("campaign_id must be at most %d characters", rewards.MaxIDLength)
	}

	rewardID := strings.TrimSpace(req.RewardID)
	if rewardID == "" {
		return fmt.Errorf("reward_id is required")
	}

	if strings.ContainsRune(rewardID, '\x00') {
		return fmt.Errorf("reward_id must not contain NUL characters")
	}

	if utf8.RuneCountInString(rewardID) > rewards.MaxIDLength {
		return fmt.Errorf("reward_id must be at most %d characters", rewards.MaxIDLength)
	}

	return nil
}

func logCreateClaimError(r *http.Request, logger *slog.Logger, err error) {
	if logger == nil {
		return
	}

	level := slog.LevelError
	errorClass := "unexpected"
	switch {
	case errors.Is(err, rewards.ErrUnavailable):
		level = slog.LevelWarn
		errorClass = "unavailable"
	case errors.Is(err, rewards.ErrInternal):
		errorClass = "internal"
	default:
		if rewards.IsValidationError(err) ||
			errors.Is(err, rewards.ErrDuplicateClaim) ||
			errors.Is(err, rewards.ErrIdempotencyKeyReused) ||
			errors.Is(err, rewards.ErrIdempotencyInProgress) {
			return
		}
	}

	logger.Log(
		r.Context(),
		level,
		"reward claim request failed",
		slog.String("request_id", requestIDFromRequest(r)),
		slog.String("operation", "reward_claim_create"),
		slog.String("error_class", errorClass),
	)
}

func writeCreateClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rewards.ErrDuplicateClaim):
		writeError(w, http.StatusConflict, errorCodeRewardAlreadyClaimed, "Reward has already been claimed")
	case errors.Is(err, rewards.ErrIdempotencyKeyReused):
		writeError(w, http.StatusConflict, errorCodeIdempotencyKeyReused, "Idempotency-Key was reused with a different request payload")
	case errors.Is(err, rewards.ErrIdempotencyInProgress):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusConflict, errorCodeIdempotencyInProgress, "A request with this Idempotency-Key is still processing")
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
