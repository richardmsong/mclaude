// oauth_exchange.go implements POST /api/auth/oauth/exchange (ADR-0092).
// Proxies the OAuth PKCE token exchange to claude.ai/oauth/token server-side,
// avoiding CORS issues with a direct browser-to-Anthropic POST.
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
)

const anthropicTokenEndpoint = "https://claude.ai/oauth/token"

// oauthExchangeHTTPClient is the HTTP client used to call claude.ai/oauth/token.
// Replaceable in tests.
var oauthExchangeHTTPClient httpDoer = http.DefaultClient

// httpDoer is a minimal interface satisfied by *http.Client, allowing test substitution.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// OAuthExchangeRequest is the body accepted by POST /api/auth/oauth/exchange.
type OAuthExchangeRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
	ClientID     string `json:"client_id"`
}

// OAuthExchangeResponse is returned on a successful token exchange.
type OAuthExchangeResponse struct {
	AccessToken string `json:"access_token"`
}

// OAuthExchangeErrorResponse is returned when claude.ai returns an error.
type OAuthExchangeErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// handleOAuthExchange handles POST /api/auth/oauth/exchange.
//
// The SPA calls this endpoint after receiving the authorization code from the
// popup callback. CP forwards a form-encoded token exchange request to
// claude.ai/oauth/token and returns the access_token to the SPA.
//
// Auth: requires valid mclaude user JWT (enforced by authMiddleware in RegisterRoutes).
// CP does not store the token — it is passed through to the SPA.
func (s *Server) handleOAuthExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req OAuthExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Code == "" || req.CodeVerifier == "" || req.RedirectURI == "" || req.ClientID == "" {
		http.Error(w, "code, code_verifier, redirect_uri, and client_id are required", http.StatusBadRequest)
		return
	}

	// Build the form-encoded body for claude.ai/oauth/token.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("code_verifier", req.CodeVerifier)
	form.Set("redirect_uri", req.RedirectURI)
	form.Set("client_id", req.ClientID)

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		anthropicTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		log.Error().Err(err).Msg("handleOAuthExchange: build upstream request")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	upstreamReq.Header.Set("Accept", "application/json")

	resp, err := oauthExchangeHTTPClient.Do(upstreamReq)
	if err != nil {
		log.Error().Err(err).Msg("handleOAuthExchange: upstream request failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(OAuthExchangeErrorResponse{ //nolint:errcheck
			Error:            "upstream_error",
			ErrorDescription: "token exchange failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("handleOAuthExchange: read upstream response")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// On success (HTTP 200 from claude.ai), extract access_token and return it.
	if resp.StatusCode == http.StatusOK {
		var upstreamResp map[string]any
		if err := json.Unmarshal(body, &upstreamResp); err != nil {
			log.Error().Err(err).Msg("handleOAuthExchange: parse upstream success response")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		accessToken, _ := upstreamResp["access_token"].(string)
		if accessToken == "" {
			log.Error().Msg("handleOAuthExchange: upstream returned 200 but access_token missing")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(OAuthExchangeErrorResponse{ //nolint:errcheck
				Error:            "missing_access_token",
				ErrorDescription: "upstream returned 200 but access_token was absent",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OAuthExchangeResponse{AccessToken: accessToken}) //nolint:errcheck
		return
	}

	// Non-200 from claude.ai: forward the error response.
	// Parse to extract error/error_description if available.
	var upstreamErr OAuthExchangeErrorResponse
	if jsonErr := json.Unmarshal(body, &upstreamErr); jsonErr != nil || upstreamErr.Error == "" {
		upstreamErr = OAuthExchangeErrorResponse{
			Error:            "token_exchange_failed",
			ErrorDescription: "upstream returned " + resp.Status,
		}
	}

	log.Warn().
		Int("upstream_status", resp.StatusCode).
		Str("error", upstreamErr.Error).
		Msg("handleOAuthExchange: upstream token exchange failed")

	w.Header().Set("Content-Type", "application/json")
	// Map common upstream error status codes to appropriate CP status codes.
	// 400 Bad Request → 400 (bad code/verifier/etc.)
	// 401 Unauthorized → 401
	// 4xx (other) → 400
	// 5xx → 502
	outStatus := http.StatusBadRequest
	if resp.StatusCode == http.StatusUnauthorized {
		outStatus = http.StatusUnauthorized
	} else if resp.StatusCode >= 500 {
		outStatus = http.StatusBadGateway
	}
	w.WriteHeader(outStatus)
	json.NewEncoder(w).Encode(upstreamErr) //nolint:errcheck
}
