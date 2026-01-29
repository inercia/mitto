//go:build !darwin

package secrets

func init() {
	// Initialize the package-level store with NoopStore on non-macOS platforms
	store = &NoopStore{}
}
