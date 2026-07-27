package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// Device-flow poll errors, following RFC 8628 so the wire vocabulary is one
// people already know from `gh auth login` and friends.
const (
	errAuthorizationPending = "authorization_pending"
	errSlowDown             = "slow_down"
	errAccessDenied         = "access_denied"
	errExpiredToken         = "expired_token"
)

// StartDeviceRequest is the body for POST /auth/device.
type StartDeviceRequest struct {
	ClientName      string   `json:"client_name,omitempty"`
	RequestedScopes []string `json:"requested_scopes,omitempty"`
}

// StartDeviceResponse mirrors the RFC 8628 device authorization response.
type StartDeviceResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// PollDeviceRequest is the body for POST /auth/device/token.
type PollDeviceRequest struct {
	DeviceCode string `json:"device_code"`
}

// PollDeviceResponse carries the minted token once an operator has approved
// the request. It is returned exactly once.
type PollDeviceResponse struct {
	Token       string        `json:"token"`
	TokenPrefix string        `json:"token_prefix"`
	Info        TokenResponse `json:"info"`
}

// DeviceRequestResponse is the approval UI's view of a pending request. It
// never exposes the device code or the issued token.
type DeviceRequestResponse struct {
	UserCode        string   `json:"user_code"`
	ClientName      string   `json:"client_name,omitempty"`
	ClientIP        string   `json:"client_ip,omitempty"`
	RequestedScopes []string `json:"requested_scopes,omitempty"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	ExpiresAt       string   `json:"expires_at"`
	ApprovedBy      string   `json:"approved_by,omitempty"`
}

// ApproveDeviceRequestBody is the body for POST /auth/device/{user_code}/approve.
// The requesting device only ever suggests scopes; what it gets is whatever
// the approver decides here.
type ApproveDeviceRequestBody struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Expires string   `json:"expires,omitempty"`
}

// startDeviceAuth begins a device authorization. Unauthenticated by design:
// the whole point is that the caller has no credentials yet.
func (s *Server) startDeviceAuth(w http.ResponseWriter, r *http.Request) {
	var req StartDeviceRequest
	// An empty body is fine — client_name is a convenience for the approver.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if len(req.RequestedScopes) > 0 {
		if err := auth.ValidateScopes(req.RequestedScopes); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	deviceCode, hash, err := auth.GenerateDeviceCode()
	if err != nil {
		writeInternalError(w, "generate device code", err)
		return
	}

	// User codes are short enough that collisions are conceivable; retry a
	// few times rather than failing the login on bad luck.
	var dr *meta.DeviceRequest
	for attempt := 0; attempt < 5; attempt++ {
		userCode, err := auth.GenerateUserCode()
		if err != nil {
			writeInternalError(w, "generate user code", err)
			return
		}
		candidate := &meta.DeviceRequest{
			DeviceCodeHash:  hash,
			UserCode:        auth.NormalizeUserCode(userCode),
			ClientName:      trimTo(req.ClientName, 64),
			ClientIP:        getClientIP(r),
			RequestedScopes: req.RequestedScopes,
			ExpiresAt:       time.Now().Add(auth.DeviceCodeTTL),
		}
		if err := s.store.CreateDeviceRequest(r.Context(), candidate); err == nil {
			dr = candidate
			break
		} else if attempt == 4 {
			writeInternalError(w, "create device request", err)
			return
		}
	}

	base := publicBaseURL(r)
	verify := base + "/device"
	writeJSON(w, http.StatusCreated, StartDeviceResponse{
		DeviceCode:              deviceCode,
		UserCode:                auth.FormatUserCode(dr.UserCode),
		VerificationURI:         verify,
		VerificationURIComplete: verify + "?code=" + auth.FormatUserCode(dr.UserCode),
		ExpiresIn:               int(auth.DeviceCodeTTL.Seconds()),
		Interval:                int(auth.DevicePollInterval.Seconds()),
	})
}

// pollDeviceAuth is the CLI's polling endpoint. Also unauthenticated: the
// device code itself is the credential.
func (s *Server) pollDeviceAuth(w http.ResponseWriter, r *http.Request) {
	var req PollDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "device_code is required")
		return
	}

	dr, err := s.store.GetDeviceRequestByCodeHash(r.Context(), auth.HashToken(req.DeviceCode))
	if err != nil {
		writeInternalError(w, "lookup device request", err)
		return
	}
	// An unknown code and an expired one are the same answer: start over.
	if dr == nil {
		writeError(w, http.StatusBadRequest, errExpiredToken)
		return
	}

	now := time.Now()
	// Enforce the advertised interval, with a little slack for clock jitter
	// and network latency.
	if dr.LastPolledAt != nil && now.Sub(*dr.LastPolledAt) < auth.DevicePollInterval-time.Second {
		writeError(w, http.StatusBadRequest, errSlowDown)
		return
	}
	if err := s.store.TouchDeviceRequest(r.Context(), dr.ID, now); err != nil {
		log.Printf("device: touch request %d: %v", dr.ID, err)
	}

	if dr.Expired(now) {
		writeError(w, http.StatusBadRequest, errExpiredToken)
		return
	}

	switch dr.Status {
	case meta.DeviceStatusDenied:
		writeError(w, http.StatusBadRequest, errAccessDenied)
		return
	case meta.DeviceStatusPending:
		writeError(w, http.StatusBadRequest, errAuthorizationPending)
		return
	}

	plain, err := s.store.ConsumeDeviceToken(r.Context(), dr.ID)
	if err != nil {
		writeInternalError(w, "consume device token", err)
		return
	}
	if plain == "" {
		// Already collected — a replayed poll must not hand it out again.
		writeError(w, http.StatusBadRequest, errExpiredToken)
		return
	}

	var info TokenResponse
	if tok, err := s.store.GetTokenByHash(r.Context(), auth.HashToken(plain)); err == nil && tok != nil {
		info = tokenView(*tok)
	}
	writeJSON(w, http.StatusOK, PollDeviceResponse{
		Token:       plain,
		TokenPrefix: auth.DisplayPrefix(plain),
		Info:        info,
	})
}

// listDeviceRequests returns everything currently awaiting approval.
func (s *Server) listDeviceRequests(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	reqs, err := s.store.ListPendingDeviceRequests(r.Context())
	if err != nil {
		writeInternalError(w, "list device requests", err)
		return
	}
	out := make([]DeviceRequestResponse, 0, len(reqs))
	for _, d := range reqs {
		out = append(out, deviceView(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// getDeviceRequest shows one pending request to the approver.
func (s *Server) getDeviceRequest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	dr, ok := s.lookupUserCode(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, deviceView(*dr))
}

// approveDeviceRequest mints a token for a waiting device.
func (s *Server) approveDeviceRequest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	dr, ok := s.lookupUserCode(w, r)
	if !ok {
		return
	}
	if dr.Status != meta.DeviceStatusPending {
		writeError(w, http.StatusConflict, "device request is no longer pending")
		return
	}

	var body ApproveDeviceRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		body.Name = deviceTokenName(dr)
	}
	if err := auth.ValidateScopes(body.Scopes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	expiresAt, err := parseExpires(body.Expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	approver := creatorOf(r)
	plain, tok, err := s.issueToken(r.Context(), body.Name, body.Scopes, expiresAt, approver)
	if err != nil {
		writeInternalError(w, "issue token", err)
		return
	}
	if err := s.store.ApproveDeviceRequest(r.Context(), dr.ID, tok.ID, plain, approver); err != nil {
		// The token exists but nobody can collect it; revoke rather than
		// leaving a live credential with no owner.
		if rErr := s.store.RevokeToken(r.Context(), tok.TokenPrefix); rErr != nil {
			log.Printf("device: orphaned token %s could not be revoked: %v", tok.TokenPrefix, rErr)
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	log.Printf("device: %s approved %s for %q (scopes %s)",
		approver, auth.FormatUserCode(dr.UserCode), dr.ClientName, strings.Join(body.Scopes, ","))

	writeJSON(w, http.StatusOK, tokenView(*tok))
}

// denyDeviceRequest rejects a waiting device.
func (s *Server) denyDeviceRequest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	dr, ok := s.lookupUserCode(w, r)
	if !ok {
		return
	}
	if err := s.store.DenyDeviceRequest(r.Context(), dr.ID, creatorOf(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lookupUserCode resolves the {user_code} URL param, writing the appropriate
// error response and returning false when it can't.
func (s *Server) lookupUserCode(w http.ResponseWriter, r *http.Request) (*meta.DeviceRequest, bool) {
	raw := chi.URLParam(r, "user_code")
	if !auth.ValidUserCode(raw) {
		writeError(w, http.StatusBadRequest, "invalid user code")
		return nil, false
	}
	dr, err := s.store.GetDeviceRequestByUserCode(r.Context(), auth.NormalizeUserCode(raw))
	if err != nil {
		writeInternalError(w, "lookup device request", err)
		return nil, false
	}
	if dr == nil || dr.Expired(time.Now()) {
		writeError(w, http.StatusNotFound, "no such device request")
		return nil, false
	}
	return dr, true
}

// purgeExpiredDeviceRequests drops rows for requests nobody completed.
func (s *Server) purgeExpiredDeviceRequests(ctx context.Context) {
	n, err := s.store.DeleteExpiredDeviceRequests(ctx)
	if err != nil {
		log.Printf("device: purge expired requests: %v", err)
		return
	}
	if n > 0 {
		log.Printf("device: purged %d expired authorization request(s)", n)
	}
}

// publicBaseURL reconstructs the scheme+host a browser would use to reach
// this server, so the verification URI we hand the CLI is one the operator
// can actually open. Behind a reverse proxy the inbound connection is plain
// HTTP, so X-Forwarded-Proto is what tells us the truth.
func publicBaseURL(r *http.Request) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func deviceView(d meta.DeviceRequest) DeviceRequestResponse {
	return DeviceRequestResponse{
		UserCode:        auth.FormatUserCode(d.UserCode),
		ClientName:      d.ClientName,
		ClientIP:        d.ClientIP,
		RequestedScopes: d.RequestedScopes,
		Status:          d.Status,
		CreatedAt:       d.CreatedAt.Format(time.RFC3339),
		ExpiresAt:       d.ExpiresAt.Format(time.RFC3339),
		ApprovedBy:      d.ApprovedBy,
	}
}

// deviceTokenName is the fallback token name when the approver doesn't supply
// one: recognisable in `auth list-tokens` without being a secret.
func deviceTokenName(d *meta.DeviceRequest) string {
	if d.ClientName != "" {
		return d.ClientName
	}
	return "device-" + auth.FormatUserCode(d.UserCode)
}

func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
