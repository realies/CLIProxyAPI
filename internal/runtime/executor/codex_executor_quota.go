package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	codexQuotaRedeemTimeout       = 30 * time.Second
	codexQuotaRedeemMaxBodyBytes  = 64 << 10
	codexQuotaRedeemMaxErrorBytes = 512
)

// codexRateLimitResetCreditsConsumeURL spends one rate-limit reset credit of a
// ChatGPT-plan credential. It is not under the Codex responses base URL, so a
// custom base_url never redirects it; tests point it at a local server.
var codexRateLimitResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"

// RedeemQuotaReset implements cliproxyauth.QuotaRedeemer. It sends the consume
// request the Codex CLI sends, with the credential's own token, account id,
// proxy and TLS profile, and returns the provider's answer. A non-2xx answer is
// an error so callers never clear local cooldown state for a credential that
// is still limited upstream.
func (e *CodexExecutor) RedeemQuotaReset(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.QuotaRedeemResult, error) {
	if auth == nil {
		return nil, fmt.Errorf("codex executor: auth is nil")
	}
	if codexAuthUsesAPIKey(auth) {
		return nil, fmt.Errorf("codex executor: api-key credentials carry no reset credits: %w", cliproxyauth.ErrQuotaRedeemUnsupported)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, codexQuotaRedeemTimeout)
	defer cancel()

	payload, errMarshal := json.Marshal(map[string]string{"redeem_request_id": uuid.NewString()})
	if errMarshal != nil {
		return nil, errMarshal
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, codexRateLimitResetCreditsConsumeURL, bytes.NewReader(payload))
	if errRequest != nil {
		return nil, errRequest
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", codexOriginator)
	cfgUserAgent, _ := codexHeaderDefaults(e.cfg, auth)
	ensureHeaderWithConfigPrecedence(req.Header, nil, "User-Agent", cfgUserAgent, codexUserAgent)
	if accountID, ok := auth.Metadata["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	resp, errDo := e.HttpRequest(ctx, auth, req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() { _ = resp.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, codexQuotaRedeemMaxBodyBytes))
	if errRead != nil {
		return nil, errRead
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > codexQuotaRedeemMaxErrorBytes {
			detail = detail[:codexQuotaRedeemMaxErrorBytes]
		}
		return nil, fmt.Errorf("codex executor: reset credit redeem returned HTTP %d: %s", resp.StatusCode, detail)
	}
	result := &cliproxyauth.QuotaRedeemResult{StatusCode: resp.StatusCode}
	if json.Valid(body) {
		result.Body = json.RawMessage(body)
	}
	return result, nil
}
