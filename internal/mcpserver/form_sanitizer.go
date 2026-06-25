// Package mcpserver provides MCP (Model Context Protocol) servers for Mitto.
// This file provides HTML sanitization for the mitto_ui_form tool.
//
// The sanitizer uses a strict whitelist approach: only form-related HTML elements
// and safe attributes are allowed. Everything else (scripts, styles, event handlers,
// external resources, iframes) is stripped.
package mcpserver

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// maxFormHTMLSize is the maximum size of form HTML content (32KB).
const maxFormHTMLSize = 32 * 1024

// mittoFilePathRegex matches safe workspace-relative file paths for data-mitto-file.
// Enforces: does not start with '/', uses only safe charset [A-Za-z0-9._/-].
// Path traversal via ".." is additionally enforced in the post-sanitization pass
// because RE2 cannot express "no two consecutive dots" without lookahead.
var mittoFilePathRegex = regexp.MustCompile(`^[A-Za-z0-9._-][A-Za-z0-9._/-]*$`)

// mittoLineRegex matches a positive line number (one or more digits) for data-mitto-line.
var mittoLineRegex = regexp.MustCompile(`^[0-9]+$`)

// mittoFileAttrRegex finds data-mitto-file attributes for post-sanitization validation.
var mittoFileAttrRegex = regexp.MustCompile(`\bdata-mitto-file="([^"]*)"`)

// formSanitizer is the shared bluemonday policy for form HTML.
// It is safe for concurrent use.
var formSanitizer = createFormSanitizer()

// createFormSanitizer builds a strict bluemonday policy that allows only
// form-related HTML elements and safe attributes.
func createFormSanitizer() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// --- Allowed form elements ---
	p.AllowElements(
		"label", "input", "select", "option", "textarea",
		"fieldset", "legend", "optgroup",
	)

	// --- Allowed layout/text elements (for form structure) ---
	p.AllowElements(
		"div", "span", "p", "br", "hr",
		"h3", "h4", "h5", "h6",
		"strong", "em", "small",
		"ul", "ol", "li",
	)

	// --- Attributes for form elements ---

	// label: for (associates with input id)
	p.AllowAttrs("for").OnElements("label")

	// Allow attribute-less <label> to survive. bluemonday drops <label> with no
	// attributes (unlike <div>/<p>/<strong>, which it keeps by default). Without
	// this, the recommended option markup — <label><input type="checkbox"> text
	// </label> with no "for" — has its <label> stripped, collapsing each option to
	// bare inline input+text so multiple options share a line. Keeping the label
	// (block-styled in CSS) puts each option on its own row.
	p.AllowNoAttrs().OnElements("label")

	// input: core form attributes
	p.AllowAttrs(
		"type", "name", "value", "placeholder",
		"required", "disabled", "readonly", "checked",
		"min", "max", "step", "maxlength", "pattern",
		"id", "size", "multiple",
	).OnElements("input")

	// select: name and basic attrs
	p.AllowAttrs("name", "id", "required", "disabled", "multiple", "size").OnElements("select")

	// option / optgroup
	p.AllowAttrs("value", "selected", "disabled").OnElements("option")
	p.AllowAttrs("label", "disabled").OnElements("optgroup")

	// textarea
	p.AllowAttrs("name", "id", "placeholder", "required", "disabled", "readonly",
		"rows", "cols", "maxlength").OnElements("textarea")

	// fieldset / legend
	p.AllowAttrs("disabled").OnElements("fieldset")

	// General: id for label-input association
	p.AllowAttrs("id").OnElements("div", "span", "p", "fieldset")

	// data-mitto-file / data-mitto-line: inert file-link markers, wired by trusted
	// frontend code to open the internal file viewer (path + line). Allowed only on
	// span and label; href/src/other data-* remain fully banned.
	// ".." path traversal is additionally enforced in the post-sanitization pass.
	p.AllowAttrs("data-mitto-file").Matching(mittoFilePathRegex).OnElements("span", "label")
	p.AllowAttrs("data-mitto-line").Matching(mittoLineRegex).OnElements("span", "label")

	// --- Explicitly NOT allowed ---
	// No: script, style, iframe, object, embed, link, meta, img, a, form, button
	// No: on* event handlers (bluemonday strips these by default)
	// No: style attribute (no inline CSS)
	// No: class attribute (prevents UI spoofing)
	// No: href, src, action attributes
	// No: javascript: or data: URLs
	// No: data-* attributes except data-mitto-file and data-mitto-line (span/label only)

	return p
}

// isMittoFilePathUnsafe returns true if a data-mitto-file path value should be
// rejected. This is defense-in-depth after bluemonday's charset regex and
// specifically catches ".." traversal that RE2 cannot express.
func isMittoFilePathUnsafe(path string) bool {
	// Absolute path
	if strings.HasPrefix(path, "/") {
		return true
	}
	// URL schemes (charset regex blocks ":" but check defensively)
	if strings.Contains(path, "://") {
		return true
	}
	lp := strings.ToLower(path)
	for _, scheme := range []string{"javascript:", "data:", "mailto:", "file:"} {
		if strings.HasPrefix(lp, scheme) {
			return true
		}
	}
	// Backslash (charset regex blocks it, but check defensively)
	if strings.Contains(path, "\\") {
		return true
	}
	// Path traversal: any ".." segment
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// allowedInputTypes are the input types we accept. Others are stripped to type="text".
var allowedInputTypes = map[string]bool{
	"text": true, "number": true, "email": true, "url": true,
	"tel": true, "password": true, "date": true, "time": true,
	"checkbox": true, "radio": true, "hidden": true,
	"color": true, "range": true,
}

// inputTypeRegex matches type="..." in input elements.
var inputTypeRegex = regexp.MustCompile(`(?i)<input\b[^>]*\btype\s*=\s*["']([^"']*)["'][^>]*>`)

// bareOptionRegex matches a checkbox/radio <input> that directly follows inline
// text (a non-'>' character) rather than a tag boundary. Agents frequently list
// each option as a bare <input> immediately followed by its label text, without
// wrapping the pair in a <label> and without a <br> between options. Such options
// flow inline and share a line. We insert a <br> before these inputs so each
// option starts on its own row. Inputs that already follow a tag boundary (the
// captured char is '>' — e.g. <label>, <br>, <p>, <div>, </label>) are left
// untouched because those cases already break onto their own line.
var bareOptionRegex = regexp.MustCompile(`(?i)([^>\s])(\s*)(<input\b[^>]*\btype\s*=\s*["'](?:checkbox|radio)["'][^>]*>)`)

// sanitizeFormHTML sanitizes the provided HTML, allowing only form-related elements.
// Returns an error if the HTML is empty or exceeds the size limit.
func sanitizeFormHTML(html string) (string, error) {
	html = strings.TrimSpace(html)
	if html == "" {
		return "", fmt.Errorf("html is required")
	}
	if len(html) > maxFormHTMLSize {
		return "", fmt.Errorf("html exceeds maximum size of %dKB (got %d bytes)", maxFormHTMLSize/1024, len(html))
	}

	// Strip any <form> wrapper tags — we handle submission ourselves.
	html = regexp.MustCompile(`(?i)</?form[^>]*>`).ReplaceAllString(html, "")

	// Strip <button> and <input type="submit"> — we provide our own submit/cancel.
	html = regexp.MustCompile(`(?i)<button[^>]*>.*?</button>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']submit["'][^>]*/?\s*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']reset["'][^>]*/?\s*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']button["'][^>]*/?\s*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']image["'][^>]*/?\s*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']file["'][^>]*/?\s*>`).ReplaceAllString(html, "")

	// Apply bluemonday sanitization
	sanitized := formSanitizer.Sanitize(html)

	// Post-sanitization: strip data-mitto-file attributes with unsafe values
	// (path traversal via "..", absolute paths, schemes). This is defense-in-depth;
	// the bluemonday charset regex already rejects most dangerous patterns except "..".
	sanitized = mittoFileAttrRegex.ReplaceAllStringFunc(sanitized, func(match string) string {
		sub := mittoFileAttrRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		if isMittoFilePathUnsafe(sub[1]) {
			return ""
		}
		return match
	})

	// Post-sanitization: validate input types and strip unknown ones
	sanitized = inputTypeRegex.ReplaceAllStringFunc(sanitized, func(match string) string {
		sub := inputTypeRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		inputType := strings.ToLower(sub[1])
		if !allowedInputTypes[inputType] {
			// Replace with type="text"
			return strings.Replace(match, sub[1], "text", 1)
		}
		return match
	})

	// Put each bare checkbox/radio option on its own line. See bareOptionRegex.
	sanitized = bareOptionRegex.ReplaceAllString(sanitized, "${1}${2}<br>${3}")

	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return "", fmt.Errorf("html contained no allowed form elements after sanitization")
	}

	return sanitized, nil
}
