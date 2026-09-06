package management

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Quota exceeded toggles
func (h *Handler) GetSwitchProject(c *gin.Context) {
	c.JSON(200, gin.H{"switch-project": h.cfg.QuotaExceeded.SwitchProject})
}
func (h *Handler) PutSwitchProject(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.QuotaExceeded.SwitchProject = v })
}

func (h *Handler) GetSwitchPreviewModel(c *gin.Context) {
	c.JSON(200, gin.H{"switch-preview-model": h.cfg.QuotaExceeded.SwitchPreviewModel})
}
func (h *Handler) PutSwitchPreviewModel(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.QuotaExceeded.SwitchPreviewModel = v })
}

// ResetQuota clears quota/cooldown routing state for one auth index. With
// "redeem": true it first spends one of the credential's provider-side reset
// credits through the provider executor, so the upstream limit and the local
// cooldown are lifted together; a refused redeem leaves local state untouched.
func (h *Handler) ResetQuota(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req struct {
		AuthIndex string `json:"auth_index"`
		Redeem    bool   `json:"redeem"`
	}
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return
	}

	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}

	var redeemed *coreauth.QuotaRedeemResult
	if req.Redeem {
		executor, _ := h.authManager.Executor(auth.Provider)
		redeemer, ok := executor.(coreauth.QuotaRedeemer)
		if !ok {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("provider %q has no quota reset credits to redeem", auth.Provider)})
			return
		}
		result, errRedeem := redeemer.RedeemQuotaReset(c.Request.Context(), auth)
		if errRedeem != nil {
			status := http.StatusBadGateway
			if errors.Is(errRedeem, coreauth.ErrQuotaRedeemUnsupported) {
				status = http.StatusUnprocessableEntity
			}
			c.JSON(status, gin.H{"error": fmt.Sprintf("failed to redeem quota reset: %v", errRedeem)})
			return
		}
		redeemed = result
	}

	updated, models, errReset := h.authManager.ResetQuota(c.Request.Context(), auth.ID)
	if errReset != nil {
		c.JSON(http.StatusInternalServerError, withRedeem(gin.H{"error": fmt.Sprintf("failed to reset quota: %v", errReset)}, redeemed))
		return
	}
	if updated == nil {
		c.JSON(http.StatusNotFound, withRedeem(gin.H{"error": "auth not found"}, redeemed))
		return
	}
	updated.EnsureIndex()

	c.JSON(http.StatusOK, withRedeem(gin.H{
		"status":     "ok",
		"auth_index": updated.Index,
		"models":     models,
	}, redeemed))
}

// withRedeem attaches the provider's answer to every response sent after a
// credit was spent, so a failed local clear never hides the redeem.
func withRedeem(body gin.H, redeemed *coreauth.QuotaRedeemResult) gin.H {
	if redeemed != nil {
		body["redeem"] = redeemed
	}
	return body
}
