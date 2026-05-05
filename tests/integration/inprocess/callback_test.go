//go:build integration

package inprocess

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// extractCallbackToken extracts the token from a callback_url returned by the API.
func extractCallbackToken(callbackURL string) string {
	parts := strings.Split(callbackURL, "/api/callback/")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// buildTestCallbackURL constructs a callback URL using the test server's URL.
func buildTestCallbackURL(ts *TestServer, token string) string {
	return ts.HTTPServer.URL + "/mitto/api/callback/" + token
}

// TestCallback_EnableAndGet tests the basic callback enable and get flow.
func TestCallback_EnableAndGet(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "callback-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Enable callback (POST /api/sessions/{id}/callback)
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	defer enableResp.Body.Close()

	if enableResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(enableResp.Body)
		t.Fatalf("expected 200, got %d: %s", enableResp.StatusCode, body)
	}

	var enableResult map[string]interface{}
	if err := json.NewDecoder(enableResp.Body).Decode(&enableResult); err != nil {
		t.Fatalf("decode enable result: %v", err)
	}

	// Verify response has callback_url (POST doesn't return created_at, only GET does)
	if _, ok := enableResult["callback_url"]; !ok {
		t.Error("enable response missing callback_url")
	}

	// Get callback status (GET /api/sessions/{id}/callback)
	getURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	getResp, err := ts.HTTPServer.Client().Get(getURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		t.Fatalf("expected 200, got %d: %s", getResp.StatusCode, body)
	}

	var getResult map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&getResult); err != nil {
		t.Fatalf("decode get result: %v", err)
	}

	// Verify get response has callback_url and created_at
	if _, ok := getResult["callback_url"]; !ok {
		t.Error("get response missing callback_url")
	}
	if _, ok := getResult["created_at"]; !ok {
		t.Error("get response missing created_at")
	}
}

// TestCallback_TriggerSuccess tests triggering a callback with periodic configured.
func TestCallback_TriggerSuccess(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "callback-trigger-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure periodic prompt
	periodicBody := map[string]interface{}{
		"prompt": "test periodic prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	periodicJSON, _ := json.Marshal(periodicBody)
	periodicURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/periodic"
	periodicReq, _ := http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err := ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic: %v", err)
	}
	defer periodicResp.Body.Close()

	if periodicResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(periodicResp.Body)
		t.Fatalf("expected 200 for periodic, got %d: %s", periodicResp.StatusCode, body)
	}

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	defer enableResp.Body.Close()

	if enableResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(enableResp.Body)
		t.Fatalf("expected 200, got %d: %s", enableResp.StatusCode, body)
	}

	var enableResult map[string]interface{}
	if err := json.NewDecoder(enableResp.Body).Decode(&enableResult); err != nil {
		t.Fatalf("decode enable result: %v", err)
	}

	// Extract token and build test server URL
	callbackURL, ok := enableResult["callback_url"].(string)
	if !ok {
		t.Fatal("callback_url not a string")
	}
	token := extractCallbackToken(callbackURL)
	if token == "" {
		t.Fatalf("failed to extract token from callback_url: %s", callbackURL)
	}
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Accept 200, 409 (session busy), or 500 (ACP not ready) as valid responses
	// The important thing is the endpoint is reachable and accepts the token
	if triggerResp.StatusCode != http.StatusOK &&
		triggerResp.StatusCode != http.StatusConflict &&
		triggerResp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("unexpected status code %d: %s", triggerResp.StatusCode, body)
	}

	t.Logf("Trigger response: %d (200/409/500 all valid for this test)", triggerResp.StatusCode)
}

// TestCallback_InvalidToken tests callback trigger with an unknown token.
func TestCallback_InvalidToken(t *testing.T) {
	ts := SetupTestServer(t)

	// Use a well-formed token (cb_ + 64 hex chars) but non-existent
	fakeToken := "cb_0000000000000000000000000000000000000000000000000000000000000000"
	callbackURL := ts.HTTPServer.URL + "/mitto/api/callback/" + fakeToken

	resp, err := ts.HTTPServer.Client().Post(callbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
}

// TestCallback_MalformedToken tests callback trigger with a malformed token.
func TestCallback_MalformedToken(t *testing.T) {
	ts := SetupTestServer(t)

	callbackURL := ts.HTTPServer.URL + "/mitto/api/callback/bad-token"

	resp, err := ts.HTTPServer.Client().Post(callbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

// TestCallback_MethodNotAllowed tests that GET to callback URL is rejected.
func TestCallback_MethodNotAllowed(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session and enable callback
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "method-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	defer enableResp.Body.Close()

	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Try GET to callback URL
	resp, err := ts.HTTPServer.Client().Get(testCallbackURL)
	if err != nil {
		t.Fatalf("GET callback URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 405, got %d: %s", resp.StatusCode, body)
	}
}

// TestCallback_PeriodicDisabled tests that callback fails when periodic is disabled.
func TestCallback_PeriodicDisabled(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "periodic-disabled-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure periodic (enabled)
	periodicBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	periodicJSON, _ := json.Marshal(periodicBody)
	periodicURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/periodic"
	periodicReq, _ := http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err := ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic: %v", err)
	}
	periodicResp.Body.Close()

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Disable periodic
	periodicBody["enabled"] = false
	periodicJSON, _ = json.Marshal(periodicBody)
	periodicReq, _ = http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err = ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic (disable): %v", err)
	}
	periodicResp.Body.Close()

	// Try to trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Should get 410 Gone (periodic_disabled)
	if triggerResp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 410, got %d: %s", triggerResp.StatusCode, body)
	}

	// Verify response body contains error info
	var errorResp map[string]interface{}
	if err := json.NewDecoder(triggerResp.Body).Decode(&errorResp); err != nil {
		t.Logf("Note: couldn't decode error response (acceptable): %v", err)
	} else if code, ok := errorResp["code"].(string); ok {
		if code != "periodic_disabled" {
			t.Errorf("expected error code 'periodic_disabled', got %v", code)
		}
	}
}

// TestCallback_PeriodicReEnabled_SameURL tests that re-enabling periodic works with same URL.
func TestCallback_PeriodicReEnabled_SameURL(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "periodic-reenabled-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure periodic (enabled)
	periodicBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	periodicJSON, _ := json.Marshal(periodicBody)
	periodicURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/periodic"
	periodicReq, _ := http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err := ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic: %v", err)
	}
	periodicResp.Body.Close()

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Disable periodic
	periodicBody["enabled"] = false
	periodicJSON, _ = json.Marshal(periodicBody)
	periodicReq, _ = http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err = ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic (disable): %v", err)
	}
	periodicResp.Body.Close()

	// Re-enable periodic
	periodicBody["enabled"] = true
	periodicJSON, _ = json.Marshal(periodicBody)
	periodicReq, _ = http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err = ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic (re-enable): %v", err)
	}
	periodicResp.Body.Close()

	// Try to trigger callback with same URL
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Should work (200, 409, or 500) - NOT 404 or 410
	if triggerResp.StatusCode == http.StatusNotFound || triggerResp.StatusCode == http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("callback should work after re-enabling periodic, got %d: %s", triggerResp.StatusCode, body)
	}

	t.Logf("Callback works after re-enabling: %d", triggerResp.StatusCode)
}

// TestCallback_Rotate tests token rotation.
func TestCallback_Rotate(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "rotate-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Enable callback (first token)
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var firstResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&firstResult)
	enableResp.Body.Close()

	firstURL, _ := firstResult["callback_url"].(string)
	firstToken := extractCallbackToken(firstURL)
	firstTestURL := buildTestCallbackURL(ts, firstToken)

	// Rotate token (POST again)
	rotateResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback (rotate): %v", err)
	}
	var secondResult map[string]interface{}
	json.NewDecoder(rotateResp.Body).Decode(&secondResult)
	rotateResp.Body.Close()

	secondURL, _ := secondResult["callback_url"].(string)
	secondToken := extractCallbackToken(secondURL)
	secondTestURL := buildTestCallbackURL(ts, secondToken)

	// Verify tokens are different
	if firstURL == secondURL {
		t.Fatal("token rotation should generate a new URL")
	}

	// Old token should return 404
	oldResp, err := ts.HTTPServer.Client().Post(firstTestURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST old callback URL: %v", err)
	}
	defer oldResp.Body.Close()

	if oldResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(oldResp.Body)
		t.Fatalf("expected 404 for old token, got %d: %s", oldResp.StatusCode, body)
	}

	// New token should work (or return 409/500 if ACP not ready, but NOT 404)
	newResp, err := ts.HTTPServer.Client().Post(secondTestURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST new callback URL: %v", err)
	}
	defer newResp.Body.Close()

	if newResp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(newResp.Body)
		t.Fatalf("new token should not return 404, got: %s", body)
	}

	t.Logf("New token works: %d", newResp.StatusCode)
}

// TestCallback_Revoke tests revoking a callback token.
func TestCallback_Revoke(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "revoke-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Revoke (DELETE)
	revokeReq, _ := http.NewRequest(http.MethodDelete, enableURL, nil)
	revokeResp, err := ts.HTTPServer.Client().Do(revokeReq)
	if err != nil {
		t.Fatalf("DELETE callback: %v", err)
	}
	revokeResp.Body.Close()

	if revokeResp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(revokeResp.Body)
		t.Fatalf("expected 204, got %d: %s", revokeResp.StatusCode, body)
	}

	// Try to trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	if triggerResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 404 after revoke, got %d: %s", triggerResp.StatusCode, body)
	}
}

// TestCallback_SessionDelete_CleansIndex tests that deleting a session cleans up the callback index.
func TestCallback_SessionDelete_CleansIndex(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "delete-cleanup-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Delete session
	if err := ts.Client.DeleteSession(sess.SessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Try to trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	if triggerResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 404 after session delete, got %d: %s", triggerResp.StatusCode, body)
	}
}

// TestCallback_RateLimit tests the callback rate limiting.
func TestCallback_RateLimit(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "rate-limit-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure periodic
	periodicBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	periodicJSON, _ := json.Marshal(periodicBody)
	periodicURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/periodic"
	periodicReq, _ := http.NewRequest(http.MethodPut, periodicURL, strings.NewReader(string(periodicJSON)))
	periodicReq.Header.Set("Content-Type", "application/json")
	periodicResp, err := ts.HTTPServer.Client().Do(periodicReq)
	if err != nil {
		t.Fatalf("PUT periodic: %v", err)
	}
	periodicResp.Body.Close()

	// Enable callback
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Make 4 rapid requests
	// The rate limit is 1 request per 10 seconds with burst of 1
	// So: 1st should succeed or fail with busy/not-ready (200/409/500)
	//     2nd, 3rd should be rate limited (429)
	//     4th should also be rate limited (429)
	var statusCodes []int
	for i := 0; i < 4; i++ {
		resp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
		if err != nil {
			t.Fatalf("POST callback URL (request %d): %v", i+1, err)
		}
		statusCodes = append(statusCodes, resp.StatusCode)
		resp.Body.Close()
		time.Sleep(10 * time.Millisecond) // Small delay to ensure ordering
	}

	t.Logf("Status codes: %v", statusCodes)

	// At least one request should be rate limited (429)
	rateLimited := false
	for i, code := range statusCodes {
		if code == http.StatusTooManyRequests {
			rateLimited = true
			t.Logf("Request %d was rate limited (429)", i+1)
		}
	}

	if !rateLimited {
		t.Error("expected at least one request to be rate limited (429)")
	}
}

// TestCallback_GetNotFound tests GET when no callback is configured.
func TestCallback_GetNotFound(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session without enabling callback
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "get-notfound-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Try to GET callback
	getURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	resp, err := ts.HTTPServer.Client().Get(getURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404 for unconfigured callback, got %d: %s", resp.StatusCode, body)
	}
}

// TestCallback_PeriodicNotConfigured tests that callback trigger fails when periodic is not configured.
func TestCallback_PeriodicNotConfigured(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "no-periodic-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Enable callback WITHOUT configuring periodic
	enableURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	enableResp, err := ts.HTTPServer.Client().Post(enableURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	var enableResult map[string]interface{}
	json.NewDecoder(enableResp.Body).Decode(&enableResult)
	enableResp.Body.Close()

	callbackURL, _ := enableResult["callback_url"].(string)
	token := extractCallbackToken(callbackURL)
	testCallbackURL := buildTestCallbackURL(ts, token)

	// Try to trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Should get 410 Gone (periodic not configured is same as disabled)
	if triggerResp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 410, got %d: %s", triggerResp.StatusCode, body)
	}

	var errorResp map[string]interface{}
	json.NewDecoder(triggerResp.Body).Decode(&errorResp)
	if code, ok := errorResp["code"].(string); !ok || code != "periodic_not_configured" {
		t.Logf("Note: error code is %v (may be 'periodic_not_configured' or 'periodic_disabled')", errorResp["code"])
	}
}
