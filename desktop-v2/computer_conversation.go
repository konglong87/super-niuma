package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

const computerOwnerMaxResponseBytes = 4096

var errComputerConversation = errors.New("computer use requires an accessible managed conversation")

// Resolve through our own authenticated server, not identity fields from JS.
// No proxy/redirects: the desktop token must never leave this loopback request.
func resolveComputerConversation(ctx context.Context, port int, token, rawRef string) (cu.SessionOwner, error) {
	ref, err := sessioncontrol.ParseRef(rawRef)
	if err != nil || ref.Source != sessioncontrol.SourceTenant || port <= 0 || port > 65535 || token == "" {
		return cu.SessionOwner{}, errComputerConversation
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/tenant/session-control/sessions/tenant/%s/computer-owner", port, url.PathEscape(ref.Key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return cu.SessionOwner{}, errComputerConversation
	}
	req.Header.Set("Authorization", "Bearer "+token)
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: computerRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return cu.SessionOwner{}, errComputerConversation
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return cu.SessionOwner{}, errComputerConversation
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, computerOwnerMaxResponseBytes+1))
	if err != nil || len(body) > computerOwnerMaxResponseBytes {
		return cu.SessionOwner{}, errComputerConversation
	}
	var envelope struct {
		Data cu.SessionOwner `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || !validComputerConversationOwner(envelope.Data) {
		return cu.SessionOwner{}, errComputerConversation
	}
	return envelope.Data, nil
}
func validComputerConversationOwner(o cu.SessionOwner) bool {
	return o.TenantID > 0 && o.TenantID <= math.MaxInt64 && o.UserID > 0 && o.UserID <= math.MaxInt64 && o.SessionID > 0 && o.SessionID <= math.MaxInt64
}
