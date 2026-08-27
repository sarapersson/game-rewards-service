package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewardClaimsHandlerRejectsStrictJSONBoundaryViolations(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "duplicate member",
			body:        `{"player_id":"player-123","player_id":"player-456","campaign_id":"campaign-123","reward_id":"reward-123"}`,
			wantCode:    errorCodeInvalidJSON,
			wantMessage: "Request body must be valid JSON",
		},
		{
			name:        "wrong case member",
			body:        `{"Player_ID":"player-123","campaign_id":"campaign-123","reward_id":"reward-123"}`,
			wantCode:    errorCodeInvalidRequest,
			wantMessage: "Request body contains an unknown field",
		},
		{
			name:        "null body",
			body:        `null`,
			wantCode:    errorCodeInvalidJSON,
			wantMessage: "Request body must contain a JSON object",
		},
		{
			name:        "array body",
			body:        `[]`,
			wantCode:    errorCodeInvalidJSON,
			wantMessage: "Request body must be valid JSON",
		},
		{
			name:        "json whitespace only",
			body:        " \t\r\n",
			wantCode:    errorCodeInvalidJSON,
			wantMessage: "Request body is required",
		},
		{
			name:        "unicode whitespace is not json whitespace",
			body:        "\u00a0",
			wantCode:    errorCodeInvalidJSON,
			wantMessage: "Request body must be valid JSON",
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

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			assertErrorResponse(t, rec, tt.wantCode, tt.wantMessage)
			if service.called {
				t.Fatal("service was called for invalid JSON request body")
			}
		})
	}
}
