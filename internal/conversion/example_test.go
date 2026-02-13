package conversion_test

import (
	"fmt"

	"github.com/inercia/mitto/internal/conversion"
)

// Example_urlsInBackticks demonstrates URL detection in backticks.
func Example_urlsInBackticks() {
	converter := conversion.NewConverter(
		conversion.WithFileLinks(conversion.FileLinkerConfig{
			WorkingDir: "/tmp",
			Enabled:    true,
		}),
	)

	// Example 1: Simple URL in backticks
	markdown := "Check out `https://example.com` for more info"
	html, _ := converter.Convert(markdown)
	fmt.Println("Example 1 - Simple URL:")
	fmt.Println("Input:", markdown)
	fmt.Println("Output contains clickable link:", containsLink(html, "https://example.com"))
	fmt.Println()

	// Example 2: Multiple URLs
	markdown = "Visit `https://github.com` and `https://docs.example.com`"
	html, _ = converter.Convert(markdown)
	fmt.Println("Example 2 - Multiple URLs:")
	fmt.Println("Input:", markdown)
	fmt.Println("Output contains github link:", containsLink(html, "https://github.com"))
	fmt.Println("Output contains docs link:", containsLink(html, "https://docs.example.com"))
	fmt.Println()

	// Example 3: mailto URL
	markdown = "Contact: `mailto:user@example.com`"
	html, _ = converter.Convert(markdown)
	fmt.Println("Example 3 - mailto URL:")
	fmt.Println("Input:", markdown)
	fmt.Println("Output contains mailto link:", containsLink(html, "mailto:user@example.com"))
	fmt.Println()

	// Example 4: URL in code block (should NOT be linked)
	markdown = "```\nhttps://example.com\n```"
	html, _ = converter.Convert(markdown)
	fmt.Println("Example 4 - URL in code block:")
	fmt.Println("Input: (code block with URL)")
	fmt.Println("Output contains link:", containsLink(html, "https://example.com"))

	// Output:
	// Example 1 - Simple URL:
	// Input: Check out `https://example.com` for more info
	// Output contains clickable link: true
	//
	// Example 2 - Multiple URLs:
	// Input: Visit `https://github.com` and `https://docs.example.com`
	// Output contains github link: true
	// Output contains docs link: true
	//
	// Example 3 - mailto URL:
	// Input: Contact: `mailto:user@example.com`
	// Output contains mailto link: true
	//
	// Example 4 - URL in code block:
	// Input: (code block with URL)
	// Output contains link: false
}

func containsLink(html, url string) bool {
	// Check if the HTML contains an anchor tag with the URL
	linkPattern := fmt.Sprintf(`<a href="%s"`, url)
	return len(html) > 0 && len(url) > 0 &&
		(findSubstring(html, linkPattern) || findSubstring(html, `<a href="`+url))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
