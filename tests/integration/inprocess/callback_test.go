//go:build integration

package inprocess

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/pkg/api"
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
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "callback-test"})
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

	// Verify get response reports a configured callback with its URL and timestamp.
	if configured, ok := getResult["configured"].(bool); !ok || !configured {
		t.Errorf("get response configured = %#v, want true", getResult["configured"])
	}
	if _, ok := getResult["callback_url"]; !ok {
		t.Error("get response missing callback_url")
	}
	if _, ok := getResult["created_at"]; !ok {
		t.Error("get response missing created_at")
	}
}

// TestCallback_TriggerSuccess tests triggering a callback with loop configured.
func TestCallback_TriggerSuccess(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "callback-trigger-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure loop prompt
	loopBody := map[string]interface{}{
		"prompt": "test loop prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	loopJSON, _ := json.Marshal(loopBody)
	loopURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/loop"
	loopReq, _ := http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err := ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop: %v", err)
	}
	defer loopResp.Body.Close()

	if loopResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loopResp.Body)
		t.Fatalf("expected 200 for loop, got %d: %s", loopResp.StatusCode, body)
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
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "method-test"})
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

// TestCallback_LoopDisabled tests that callback fails when loop is disabled.
func TestCallback_LoopDisabled(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "loop-disabled-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure loop (enabled)
	loopBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	loopJSON, _ := json.Marshal(loopBody)
	loopURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/loop"
	loopReq, _ := http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err := ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop: %v", err)
	}
	loopResp.Body.Close()

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

	// Disable loop
	loopBody["enabled"] = false
	loopJSON, _ = json.Marshal(loopBody)
	loopReq, _ = http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err = ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop (disable): %v", err)
	}
	loopResp.Body.Close()

	// Try to trigger callback
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Should get 410 Gone (loop_disabled)
	if triggerResp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 410, got %d: %s", triggerResp.StatusCode, body)
	}

	// Verify response body contains error info
	var errorResp map[string]interface{}
	if err := json.NewDecoder(triggerResp.Body).Decode(&errorResp); err != nil {
		t.Logf("Note: couldn't decode error response (acceptable): %v", err)
	} else if code, ok := errorResp["code"].(string); ok {
		if code != "loop_disabled" {
			t.Errorf("expected error code 'loop_disabled', got %v", code)
		}
	}
}

// TestCallback_LoopReEnabled_SameURL tests that re-enabling loop works with same URL.
func TestCallback_LoopReEnabled_SameURL(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "loop-reenabled-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure loop (enabled)
	loopBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	loopJSON, _ := json.Marshal(loopBody)
	loopURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/loop"
	loopReq, _ := http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err := ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop: %v", err)
	}
	loopResp.Body.Close()

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

	// Disable loop
	loopBody["enabled"] = false
	loopJSON, _ = json.Marshal(loopBody)
	loopReq, _ = http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err = ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop (disable): %v", err)
	}
	loopResp.Body.Close()

	// Re-enable loop
	loopBody["enabled"] = true
	loopJSON, _ = json.Marshal(loopBody)
	loopReq, _ = http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err = ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop (re-enable): %v", err)
	}
	loopResp.Body.Close()

	// Try to trigger callback with same URL
	triggerResp, err := ts.HTTPServer.Client().Post(testCallbackURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST callback URL: %v", err)
	}
	defer triggerResp.Body.Close()

	// Should work (200, 409, or 500) - NOT 404 or 410
	if triggerResp.StatusCode == http.StatusNotFound || triggerResp.StatusCode == http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("callback should work after re-enabling loop, got %d: %s", triggerResp.StatusCode, body)
	}

	t.Logf("Callback works after re-enabling: %d", triggerResp.StatusCode)
}

// TestCallback_Rotate tests token rotation.
func TestCallback_Rotate(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "rotate-test"})
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
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "revoke-test"})
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
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "delete-cleanup-test"})
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
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "rate-limit-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure loop
	loopBody := map[string]interface{}{
		"prompt": "test prompt",
		"frequency": map[string]interface{}{
			"value": 30,
			"unit":  "minutes",
		},
		"enabled": true,
	}
	loopJSON, _ := json.Marshal(loopBody)
	loopURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/loop"
	loopReq, _ := http.NewRequest(http.MethodPut, loopURL, strings.NewReader(string(loopJSON)))
	loopReq.Header.Set("Content-Type", "application/json")
	loopResp, err := ts.HTTPServer.Client().Do(loopReq)
	if err != nil {
		t.Fatalf("PUT loop: %v", err)
	}
	loopResp.Body.Close()

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

// TestCallback_GetUnconfigured tests GET when no callback is configured.
func TestCallback_GetUnconfigured(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session without enabling callback
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "get-notfound-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// An existing session without a callback is a valid status response, not a missing resource.
	getURL := ts.HTTPServer.URL + "/mitto/api/sessions/" + sess.SessionID + "/callback"
	resp, err := ts.HTTPServer.Client().Get(getURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for unconfigured callback, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode unconfigured callback result: %v", err)
	}
	if configured, ok := result["configured"].(bool); !ok || configured {
		t.Errorf("configured = %#v, want false", result["configured"])
	}
	if _, ok := result["callback_url"]; ok {
		t.Errorf("unconfigured response unexpectedly contains callback_url: %#v", result)
	}
	if _, ok := result["created_at"]; ok {
		t.Errorf("unconfigured response unexpectedly contains created_at: %#v", result)
	}
}

// TestCallback_GetMissingSession preserves a terminal 404 for a genuinely absent session.
func TestCallback_GetMissingSession(t *testing.T) {
	ts := SetupTestServer(t)

	getURL := ts.HTTPServer.URL + "/mitto/api/sessions/20260817-000000-deadbeef/callback"
	resp, err := ts.HTTPServer.Client().Get(getURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404 for missing session, got %d: %s", resp.StatusCode, body)
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode missing-session error: %v", err)
	}
	if envelope.Error.Code != "not_found" || envelope.Error.Message != "Session not found" {
		t.Errorf("error = %#v, want not_found/Session not found", envelope.Error)
	}
}

// TestCallback_LoopNotConfigured tests that callback trigger fails when loop is not configured.
func TestCallback_LoopNotConfigured(t *testing.T) {
	ts := SetupTestServer(t)

	// Create session
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "no-loop-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Enable callback WITHOUT configuring loop
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

	// Should get 410 Gone (loop not configured is same as disabled)
	if triggerResp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(triggerResp.Body)
		t.Fatalf("expected 410, got %d: %s", triggerResp.StatusCode, body)
	}

	var errorResp map[string]interface{}
	json.NewDecoder(triggerResp.Body).Decode(&errorResp)
	if code, ok := errorResp["code"].(string); !ok || code != "loop_disabled" {
		t.Logf("Note: error code is %v (expected 'loop_disabled')", errorResp["code"])
	}
}
