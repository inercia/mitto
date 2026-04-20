package mcpserver

import (
	"strings"
	"testing"
)

// =============================================================================
// sanitizeFormHTML — Allowed Elements
// =============================================================================

func TestSanitizeFormHTML_AllowsBasicFormElements(t *testing.T) {
	html := `<label for="name">Name:</label><input type="text" name="name" placeholder="Enter name" required>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<label") {
		t.Error("expected label element to be preserved")
	}
	if !strings.Contains(result, "<input") {
		t.Error("expected input element to be preserved")
	}
	if !strings.Contains(result, `name="name"`) {
		t.Error("expected name attribute to be preserved")
	}
	if !strings.Contains(result, `placeholder="Enter name"`) {
		t.Error("expected placeholder attribute to be preserved")
	}
}

func TestSanitizeFormHTML_AllowsSelectWithOptions(t *testing.T) {
	html := `<select name="color"><option value="red">Red</option><option value="blue" selected>Blue</option></select>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<select") {
		t.Error("expected select element")
	}
	if !strings.Contains(result, "<option") {
		t.Error("expected option elements")
	}
	if !strings.Contains(result, `value="red"`) {
		t.Error("expected value attribute on option")
	}
}

func TestSanitizeFormHTML_AllowsTextarea(t *testing.T) {
	html := `<textarea name="notes" rows="4" placeholder="Notes..."></textarea>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<textarea") {
		t.Error("expected textarea element")
	}
	if !strings.Contains(result, `rows="4"`) {
		t.Error("expected rows attribute")
	}
}

func TestSanitizeFormHTML_AllowsCheckboxAndRadio(t *testing.T) {
	html := `<input type="checkbox" name="agree" checked><input type="radio" name="choice" value="a">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `type="checkbox"`) {
		t.Error("expected checkbox input")
	}
	if !strings.Contains(result, `type="radio"`) {
		t.Error("expected radio input")
	}
}

func TestSanitizeFormHTML_AllowsFieldset(t *testing.T) {
	html := `<fieldset><legend>Personal Info</legend><input type="text" name="name"></fieldset>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<fieldset") {
		t.Error("expected fieldset element")
	}
	// Note: bluemonday strips <legend> tags but preserves their text content
	if !strings.Contains(result, "Personal Info") {
		t.Error("expected legend text content to be preserved")
	}
}

func TestSanitizeFormHTML_AllowsLayoutElements(t *testing.T) {
	html := `<div><p><strong>Bold</strong> and <em>italic</em></p><br><hr></div>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<div>") {
		t.Error("expected div element")
	}
	if !strings.Contains(result, "<strong>") {
		t.Error("expected strong element")
	}
	if !strings.Contains(result, "<em>") {
		t.Error("expected em element")
	}
}

func TestSanitizeFormHTML_AllowsLabelForAssociation(t *testing.T) {
	html := `<label for="email">Email:</label><input type="email" name="email" id="email">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `for="email"`) {
		t.Error("expected for attribute on label")
	}
	if !strings.Contains(result, `id="email"`) {
		t.Error("expected id attribute on input")
	}
}

func TestSanitizeFormHTML_AllowsAllValidInputTypes(t *testing.T) {
	validTypes := []string{"text", "number", "email", "url", "tel", "password", "date", "time", "checkbox", "radio", "hidden", "color", "range"}
	for _, typ := range validTypes {
		html := `<input type="` + typ + `" name="field">`
		result, err := sanitizeFormHTML(html)
		if err != nil {
			t.Fatalf("unexpected error for type %s: %v", typ, err)
		}
		if !strings.Contains(result, `type="`+typ+`"`) {
			t.Errorf("expected type=%s to be preserved, got: %s", typ, result)
		}
	}
}

// =============================================================================
// sanitizeFormHTML — Stripped / Dangerous Elements
// =============================================================================

func TestSanitizeFormHTML_StripsScriptTags(t *testing.T) {
	html := `<label>Name:</label><script>alert('xss')</script><input type="text" name="name">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<script") {
		t.Error("expected script tags to be stripped")
	}
	if strings.Contains(result, "alert") {
		t.Error("expected script content to be stripped")
	}
}

func TestSanitizeFormHTML_StripsStyleTags(t *testing.T) {
	html := `<style>body{display:none}</style><input type="text" name="x">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<style") {
		t.Error("expected style tags to be stripped")
	}
}

func TestSanitizeFormHTML_StripsIframe(t *testing.T) {
	html := `<iframe src="http://evil.com"></iframe><input type="text" name="x">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<iframe") {
		t.Error("expected iframe to be stripped")
	}
}

func TestSanitizeFormHTML_StripsEventHandlers(t *testing.T) {
	html := `<input type="text" name="x" onclick="alert(1)" onmouseover="evil()" onfocus="steal()">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(result), "onclick") {
		t.Error("expected onclick to be stripped")
	}
	if strings.Contains(strings.ToLower(result), "onmouseover") {
		t.Error("expected onmouseover to be stripped")
	}
	if strings.Contains(strings.ToLower(result), "onfocus") {
		t.Error("expected onfocus to be stripped")
	}
}

func TestSanitizeFormHTML_StripsImgTags(t *testing.T) {
	html := `<img src="http://evil.com/tracker.gif"><input type="text" name="x">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<img") {
		t.Error("expected img tags to be stripped")
	}
}

func TestSanitizeFormHTML_StripsAnchorTags(t *testing.T) {
	html := `<a href="http://evil.com">Click me</a><input type="text" name="x">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<a ") || strings.Contains(result, "<a>") {
		t.Error("expected anchor tags to be stripped")
	}
}

func TestSanitizeFormHTML_StripsFormWrapper(t *testing.T) {
	html := `<form action="http://evil.com" method="POST"><input type="text" name="x"></form>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "<form") {
		t.Error("expected form tags to be stripped")
	}
	if !strings.Contains(result, "<input") {
		t.Error("expected input inside form to be preserved")
	}
}

func TestSanitizeFormHTML_StripsSubmitButtons(t *testing.T) {
	html := `<input type="text" name="x"><input type="submit" value="Submit"><button type="submit">Go</button>`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, `type="submit"`) {
		t.Error("expected submit inputs to be stripped")
	}
	if strings.Contains(result, "<button") {
		t.Error("expected button elements to be stripped")
	}
}

func TestSanitizeFormHTML_StripsFileInputs(t *testing.T) {
	html := `<input type="file" name="upload"><input type="text" name="x">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, `type="file"`) {
		t.Error("expected file input to be stripped")
	}
}

func TestSanitizeFormHTML_StripsStyleAttribute(t *testing.T) {
	html := `<input type="text" name="x" style="position:absolute;top:0">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "style=") {
		t.Error("expected style attribute to be stripped")
	}
}

func TestSanitizeFormHTML_StripsClassAttribute(t *testing.T) {
	html := `<input type="text" name="x" class="fake-login">`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "class=") {
		t.Error("expected class attribute to be stripped")
	}
}

// =============================================================================
// sanitizeFormHTML — Error Cases
// =============================================================================

func TestSanitizeFormHTML_ErrorOnEmptyHTML(t *testing.T) {
	_, err := sanitizeFormHTML("")
	if err == nil {
		t.Fatal("expected error for empty HTML")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("expected 'required' in error, got: %v", err)
	}
}

func TestSanitizeFormHTML_ErrorOnWhitespaceOnly(t *testing.T) {
	_, err := sanitizeFormHTML("   \n\t  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only HTML")
	}
}

func TestSanitizeFormHTML_ErrorOnOversizedHTML(t *testing.T) {
	html := "<input type=\"text\" name=\"x\">" + strings.Repeat("a", maxFormHTMLSize+1)
	_, err := sanitizeFormHTML(html)
	if err == nil {
		t.Fatal("expected error for oversized HTML")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected 'exceeds' in error, got: %v", err)
	}
}

func TestSanitizeFormHTML_ErrorOnAllStripped(t *testing.T) {
	_, err := sanitizeFormHTML(`<script>alert(1)</script>`)
	if err == nil {
		t.Fatal("expected error when all content is stripped")
	}
	if !strings.Contains(err.Error(), "no allowed form elements") {
		t.Errorf("expected 'no allowed form elements' in error, got: %v", err)
	}
}

// =============================================================================
// sanitizeFormHTML — Complex / Real-World Examples
// =============================================================================

func TestSanitizeFormHTML_RealWorldDeploymentForm(t *testing.T) {
	html := `
		<div>
			<label for="env">Environment:</label>
			<select name="env" id="env" required>
				<option value="">-- Select --</option>
				<option value="staging">Staging</option>
				<option value="prod">Production</option>
			</select>
		</div>
		<div>
			<label for="version">Version Tag:</label>
			<input type="text" name="version" id="version" placeholder="v1.2.3" required>
		</div>
		<div>
			<label><input type="checkbox" name="notify" checked> Send notifications</label>
		</div>
		<div>
			<label for="notes">Release Notes:</label>
			<textarea name="notes" id="notes" rows="4" placeholder="What changed?"></textarea>
		</div>
	`
	result, err := sanitizeFormHTML(html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{
		`name="env"`, `name="version"`, `name="notify"`, `name="notes"`,
		"<select", "<textarea", `type="checkbox"`, `type="text"`,
		"<label", "<option",
	} {
		if !strings.Contains(result, expected) {
			t.Errorf("expected %q in result, got: %s", expected, result)
		}
	}
}

func TestSanitizeFormHTML_XSSPayloadsStripped(t *testing.T) {
	payloads := []struct {
		name string
		html string
		bad  string
	}{
		{"svg onload", `<svg onload="alert(1)"><input type="text" name="x">`, "<svg"},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=evil"><input type="text" name="x">`, "<meta"},
		{"link tag", `<link rel="stylesheet" href="evil.css"><input type="text" name="x">`, "<link"},
		{"base tag", `<base href="http://evil.com"><input type="text" name="x">`, "<base"},
	}
	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			result, err := sanitizeFormHTML(tc.html)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(strings.ToLower(result), tc.bad) {
				t.Errorf("expected %q to be stripped, got: %s", tc.bad, result)
			}
		})
	}
}
