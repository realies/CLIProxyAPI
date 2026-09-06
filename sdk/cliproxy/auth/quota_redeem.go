package auth

import (
	"context"
	"encoding/json"
	"errors"
)

// QuotaRedeemer lets an executor spend a provider-side rate-limit reset credit
// for one credential. Only providers that issue such credits implement it.
type QuotaRedeemer interface {
	// RedeemQuotaReset spends one credit for auth and returns the provider's
	// answer. A refusal is returned as an error, never as a result, so callers
	// can key "clear local cooldown state" on a nil error.
	RedeemQuotaReset(ctx context.Context, auth *Auth) (*QuotaRedeemResult, error)
}

// QuotaRedeemResult is the provider's answer to a redeemed reset credit. Body
// is the raw JSON response when the provider returned one, so remaining-credit
// fields reach the caller without this package knowing their shape.
type QuotaRedeemResult struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// ErrQuotaRedeemUnsupported reports that a provider or credential kind has no
// reset credits to redeem.
var ErrQuotaRedeemUnsupported = errors.New("quota reset redeem unsupported")
