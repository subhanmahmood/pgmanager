package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Device-flow poll outcomes, mirroring the server's RFC 8628 vocabulary.
const (
	deviceErrAuthorizationPending = "authorization_pending"
	deviceErrSlowDown             = "slow_down"
	deviceErrAccessDenied         = "access_denied"
	deviceErrExpiredToken         = "expired_token"
)

// ErrDeviceDenied is returned when an operator rejected the login.
var ErrDeviceDenied = errors.New("device authorization was denied")

// ErrDeviceExpired is returned when the code lapsed before anyone approved it.
var ErrDeviceExpired = errors.New("device code expired before it was approved")

// DeviceAuth is the server's answer to a device authorization request.
type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Interval returns the poll interval the server asked for, with a sane floor
// so a misconfigured server can't make us hammer it.
func (d *DeviceAuth) PollInterval() time.Duration {
	if d.Interval < 1 {
		return 5 * time.Second
	}
	return time.Duration(d.Interval) * time.Second
}

// StartDeviceAuth begins a device login. No credentials are needed — that is
// the point.
func (c *HTTPClient) StartDeviceAuth(ctx context.Context, clientName string, requestedScopes []string) (*DeviceAuth, error) {
	body := map[string]interface{}{"client_name": clientName}
	if len(requestedScopes) > 0 {
		body["requested_scopes"] = requestedScopes
	}
	var out DeviceAuth
	if err := c.do(ctx, http.MethodPost, "/auth/device", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollDeviceAuth checks once for approval. It returns:
//   - (token, nil) once an operator approved the request
//   - ("", nil) while the request is still pending
//   - ("", ErrDeviceDenied / ErrDeviceExpired) on a terminal outcome
func (c *HTTPClient) PollDeviceAuth(ctx context.Context, deviceCode string) (string, *Token, error) {
	type resp struct {
		Token string `json:"token"`
		Info  Token  `json:"info"`
	}
	var r resp
	err := c.do(ctx, http.MethodPost, "/auth/device/token", map[string]string{"device_code": deviceCode}, &r)
	if err == nil {
		return r.Token, &r.Info, nil
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "", nil, err
	}
	switch apiErr.Message {
	case deviceErrAuthorizationPending, deviceErrSlowDown:
		return "", nil, nil
	case deviceErrAccessDenied:
		return "", nil, ErrDeviceDenied
	case deviceErrExpiredToken:
		return "", nil, ErrDeviceExpired
	}
	return "", nil, err
}

// WaitForDeviceAuth polls until the request is approved, denied, or expires.
// onPending, when non-nil, is called before each wait so callers can show
// progress.
func (c *HTTPClient) WaitForDeviceAuth(ctx context.Context, d *DeviceAuth) (string, *Token, error) {
	interval := d.PollInterval()
	deadline := time.Now().Add(time.Duration(d.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(interval):
		}

		token, info, err := c.PollDeviceAuth(ctx, d.DeviceCode)
		if err != nil {
			return "", nil, err
		}
		if token != "" {
			return token, info, nil
		}
		if time.Now().After(deadline) {
			return "", nil, ErrDeviceExpired
		}
	}
}

// ListDeviceRequests returns the authorizations awaiting approval.
func (c *HTTPClient) ListDeviceRequests(ctx context.Context) ([]DeviceRequest, error) {
	var out []DeviceRequest
	if err := c.do(ctx, http.MethodGet, "/auth/devices", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeviceRequest is the approver's view of a waiting device.
type DeviceRequest struct {
	UserCode        string   `json:"user_code"`
	ClientName      string   `json:"client_name,omitempty"`
	ClientIP        string   `json:"client_ip,omitempty"`
	RequestedScopes []string `json:"requested_scopes,omitempty"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	ExpiresAt       string   `json:"expires_at"`
}

// ApproveDeviceRequest mints a token for a waiting device. The scopes given
// here are what the device actually gets, regardless of what it asked for.
func (c *HTTPClient) ApproveDeviceRequest(ctx context.Context, userCode, name string, scopes []string, expires string) (*Token, error) {
	body := map[string]interface{}{"name": name, "scopes": scopes}
	if expires != "" {
		body["expires"] = expires
	}
	var out Token
	path := fmt.Sprintf("/auth/device/%s/approve", url.PathEscape(userCode))
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DenyDeviceRequest rejects a waiting device.
func (c *HTTPClient) DenyDeviceRequest(ctx context.Context, userCode string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/auth/device/%s/deny", url.PathEscape(userCode)), nil, nil)
}
