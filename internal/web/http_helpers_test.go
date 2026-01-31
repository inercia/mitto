package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		data       interface{}
		wantStatus int
		wantBody   string
	}{
		{
			name:       "200 OK with map",
			status:     http.StatusOK,
			data:       map[string]string{"key": "value"},
			wantStatus: http.StatusOK,
			wantBody:   `{"key":"value"}`,
		},
		{
			name:       "201 Created with struct",
			status:     http.StatusCreated,
			data:       struct{ ID int }{ID: 123},
			wantStatus: http.StatusCreated,
			wantBody:   `{"ID":123}`,
		},
		{
			name:       "400 Bad Request with error",
			status:     http.StatusBadRequest,
			data:       map[string]string{"error": "invalid"},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid"}`,
		},
		{
			name:       "empty data",
			status:     http.StatusOK,
			data:       map[string]string{},
			wantStatus: http.StatusOK,
			wantBody:   `{}`,
		},
		{
			name:       "nil data",
			status:     http.StatusOK,
			data:       nil,
			wantStatus: http.StatusOK,
			wantBody:   `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSON(w, tt.status, tt.data)

			if w.Code != tt.wantStatus {
				t.Errorf("writeJSON() status = %d, want %d", w.Code, tt.wantStatus)
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("writeJSON() Content-Type = %q, want %q", contentType, "application/json")
			}

			// Trim newline added by json.Encoder
			gotBody := strings.TrimSpace(w.Body.String())
			if gotBody != tt.wantBody {
				t.Errorf("writeJSON() body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

func TestWriteJSONOK(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONOK(w, map[string]string{"status": "ok"})

	if w.Code != http.StatusOK {
		t.Errorf("writeJSONOK() status = %d, want %d", w.Code, http.StatusOK)
	}

	gotBody := strings.TrimSpace(w.Body.String())
	if gotBody != `{"status":"ok"}` {
		t.Errorf("writeJSONOK() body = %q, want %q", gotBody, `{"status":"ok"}`)
	}
}

func TestWriteJSONCreated(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONCreated(w, map[string]int{"id": 42})

	if w.Code != http.StatusCreated {
		t.Errorf("writeJSONCreated() status = %d, want %d", w.Code, http.StatusCreated)
	}

	gotBody := strings.TrimSpace(w.Body.String())
	if gotBody != `{"id":42}` {
		t.Errorf("writeJSONCreated() body = %q, want %q", gotBody, `{"id":42}`)
	}
}

func TestWriteErrorJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeErrorJSON(w, http.StatusBadRequest, "validation_error", "Field is required")

	if w.Code != http.StatusBadRequest {
		t.Errorf("writeErrorJSON() status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["error"] != "validation_error" {
		t.Errorf("writeErrorJSON() error = %q, want %q", resp["error"], "validation_error")
	}
	if resp["message"] != "Field is required" {
		t.Errorf("writeErrorJSON() message = %q, want %q", resp["message"], "Field is required")
	}
}

func TestWriteNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	writeNoContent(w)

	if w.Code != http.StatusNoContent {
		t.Errorf("writeNoContent() status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if w.Body.Len() != 0 {
		t.Errorf("writeNoContent() body should be empty, got %q", w.Body.String())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	methodNotAllowed(w)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("methodNotAllowed() status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	body := strings.TrimSpace(w.Body.String())
	if body != "Method not allowed" {
		t.Errorf("methodNotAllowed() body = %q, want %q", body, "Method not allowed")
	}
}

func TestParseJSONBody_Success(t *testing.T) {
	body := `{"name": "test", "value": 42}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var data struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	ok := parseJSONBody(w, r, &data)

	if !ok {
		t.Error("parseJSONBody() returned false, want true")
	}
	if data.Name != "test" {
		t.Errorf("parseJSONBody() Name = %q, want %q", data.Name, "test")
	}
	if data.Value != 42 {
		t.Errorf("parseJSONBody() Value = %d, want %d", data.Value, 42)
	}
	// No error response should be written
	if w.Code != http.StatusOK {
		t.Errorf("parseJSONBody() should not write status on success, got %d", w.Code)
	}
}

func TestParseJSONBody_InvalidJSON(t *testing.T) {
	body := `{invalid json}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var data map[string]string

	ok := parseJSONBody(w, r, &data)

	if ok {
		t.Error("parseJSONBody() returned true for invalid JSON, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("parseJSONBody() status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "Invalid request body") {
		t.Errorf("parseJSONBody() body should contain 'Invalid request body', got %q", w.Body.String())
	}
}

func TestParseJSONBody_EmptyBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	var data map[string]string

	ok := parseJSONBody(w, r, &data)

	if ok {
		t.Error("parseJSONBody() returned true for empty body, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("parseJSONBody() status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestParseJSONBody_TypeMismatch(t *testing.T) {
	body := `{"value": "not a number"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var data struct {
		Value int `json:"value"`
	}

	ok := parseJSONBody(w, r, &data)

	if ok {
		t.Error("parseJSONBody() returned true for type mismatch, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("parseJSONBody() status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
