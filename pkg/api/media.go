package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
)

// --- Image API ---

// ImageInfo represents an uploaded image.
type ImageInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size,omitempty"`
}

// UploadImage uploads an image to a session via multipart form.
func (c *Client) UploadImage(sessionID string, filename string, mimeType string, data []byte) (*ImageInfo, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "image",
		"filename": filename,
	}))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("upload image: create form: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("upload image: write data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("upload image: close writer: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images"),
		writer.FormDataContentType(),
		&buf,
	)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("upload image", resp)
	}

	var info ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("upload image: decode: %w", err)
	}
	return &info, nil
}

// ListImages returns the images uploaded to a session.
func (c *Client) ListImages(sessionID string) ([]ImageInfo, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("list images", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("list images", resp)
	}

	var images []ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, fmt.Errorf("list images: decode: %w", err)
	}
	return images, nil
}

// GetImage downloads the raw bytes and content type of a session image.
// The caller is responsible for closing the returned ReadCloser.
func (c *Client) GetImage(sessionID, imageID string) (io.ReadCloser, string, error) {
	req, err := c.newRequest(http.MethodGet,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images/"+url.PathEscape(imageID)),
		"", nil,
	)
	if err != nil {
		return nil, "", fmt.Errorf("get image: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, "", fmt.Errorf("get image: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		defer resp.Body.Close()
		return nil, "", &APIError{Op: "get image", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("image not found: %s", imageID),
			Details: map[string]any{"image_id": imageID}}
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, "", c.apiError("get image", resp)
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// DeleteImage removes an image from a session.
func (c *Client) DeleteImage(sessionID, imageID string) error {
	req, err := c.newRequest(http.MethodDelete,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images/"+url.PathEscape(imageID)),
		"", nil,
	)
	if err != nil {
		return fmt.Errorf("delete image: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("delete image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &APIError{Op: "delete image", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("image not found: %s", imageID),
			Details: map[string]any{"image_id": imageID}}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("delete image", resp)
	}
	return nil
}

// --- File API ---

// SessionFileInfo represents an uploaded file (named to avoid colliding with
// the pre-existing FileInfo helper type used by PromptResult.FilesRead/Written).
type SessionFileInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Category string `json:"category,omitempty"` // "text" or "binary"
}

// UploadFile uploads a file to a session via multipart form. Mirrors
// UploadImage's shape; the server accepts any of the file types documented
// in internal/session.GetFileCategory.
func (c *Client) UploadFile(sessionID string, filename string, mimeType string, data []byte) (*SessionFileInfo, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "file",
		"filename": filename,
	}))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("upload file: create form: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("upload file: write data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("upload file: close writer: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/files"),
		writer.FormDataContentType(),
		&buf,
	)
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("upload file", resp)
	}

	var info SessionFileInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("upload file: decode: %w", err)
	}
	return &info, nil
}

// ListFiles returns the files uploaded to a session.
func (c *Client) ListFiles(sessionID string) ([]SessionFileInfo, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/files"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("list files", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("list files", resp)
	}

	var files []SessionFileInfo
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("list files: decode: %w", err)
	}
	return files, nil
}

// GetFile downloads the raw bytes and content type of a session file.
// The caller is responsible for closing the returned ReadCloser.
func (c *Client) GetFile(sessionID, fileID string) (io.ReadCloser, string, error) {
	req, err := c.newRequest(http.MethodGet,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/files/"+url.PathEscape(fileID)),
		"", nil,
	)
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		defer resp.Body.Close()
		return nil, "", &APIError{Op: "get file", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("file not found: %s", fileID),
			Details: map[string]any{"file_id": fileID}}
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, "", c.apiError("get file", resp)
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// DeleteFile removes a file from a session.
func (c *Client) DeleteFile(sessionID, fileID string) error {
	req, err := c.newRequest(http.MethodDelete,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/files/"+url.PathEscape(fileID)),
		"", nil,
	)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &APIError{Op: "delete file", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("file not found: %s", fileID),
			Details: map[string]any{"file_id": fileID}}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("delete file", resp)
	}
	return nil
}
