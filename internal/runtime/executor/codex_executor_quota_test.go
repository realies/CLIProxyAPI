package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func pointCodexRedeemAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := codexRateLimitResetCreditsConsumeURL
	codexRateLimitResetCreditsConsumeURL = server.URL + "/backend-api/wham/rate-limit-reset-credits/consume"
	t.Cleanup(func() { codexRateLimitResetCreditsConsumeURL = original })
}

func codexRedeemAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       "codex-redeem",
		Provider: "codex",
		Metadata: map[string]any{"access_token": "test-token", "account_id": "acct-123"},
	}
}

func TestCodexExecutorRedeemQuotaReset_RequestShape(t *testing.T) {
	var got struct {
		method, path, authorization, accountID, contentType, userAgent string
		body                                                           map[string]string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.authorization = r.Header.Get("Authorization")
		got.accountID = r.Header.Get("Chatgpt-Account-Id")
		got.contentType = r.Header.Get("Content-Type")
		got.userAgent = r.Header.Get("User-Agent")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available_count":0}`))
	}))
	defer server.Close()
	pointCodexRedeemAt(t, server)

	result, errRedeem := NewCodexExecutor(&config.Config{}).RedeemQuotaReset(context.Background(), codexRedeemAuth())
	if errRedeem != nil {
		t.Fatalf("RedeemQuotaReset returned error: %v", errRedeem)
	}
	if got.method != http.MethodPost || got.path != "/backend-api/wham/rate-limit-reset-credits/consume" {
		t.Fatalf("request = %s %s, want POST consume path", got.method, got.path)
	}
	if got.authorization != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want the credential's bearer token", got.authorization)
	}
	if got.accountID != "acct-123" {
		t.Fatalf("Chatgpt-Account-Id = %q, want acct-123", got.accountID)
	}
	if got.contentType != "application/json" || !strings.HasPrefix(got.userAgent, "codex-tui/") {
		t.Fatalf("Content-Type = %q User-Agent = %q, want JSON with a codex-tui user agent", got.contentType, got.userAgent)
	}
	if _, errParse := uuid.Parse(got.body["redeem_request_id"]); errParse != nil {
		t.Fatalf("redeem_request_id = %q, want a UUID: %v", got.body["redeem_request_id"], errParse)
	}
	if result == nil || result.StatusCode != http.StatusOK || string(result.Body) != `{"available_count":0}` {
		t.Fatalf("result = %+v, want the provider answer passed through", result)
	}
}

func TestCodexExecutorRedeemQuotaReset_RefusalIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"no credits"}`))
	}))
	defer server.Close()
	pointCodexRedeemAt(t, server)

	result, errRedeem := NewCodexExecutor(&config.Config{}).RedeemQuotaReset(context.Background(), codexRedeemAuth())
	if errRedeem == nil || result != nil || !strings.Contains(errRedeem.Error(), "402") {
		t.Fatalf("result = %+v err = %v, want nil result and an error naming HTTP 402", result, errRedeem)
	}

	apiKeyAuth := &cliproxyauth.Auth{ID: "codex-key", Provider: "codex", Attributes: map[string]string{"api_key": "sk-test"}}
	if _, errAPIKey := NewCodexExecutor(&config.Config{}).RedeemQuotaReset(context.Background(), apiKeyAuth); !errors.Is(errAPIKey, cliproxyauth.ErrQuotaRedeemUnsupported) {
		t.Fatalf("api-key redeem error = %v, want ErrQuotaRedeemUnsupported", errAPIKey)
	}
}
