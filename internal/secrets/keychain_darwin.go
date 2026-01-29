//go:build darwin

package secrets

import (
	"errors"

	"github.com/keybase/go-keychain"
)

func init() {
	// Initialize the package-level store with KeychainStore on macOS
	store = &KeychainStore{}
}

// KeychainStore implements SecretStore using the macOS Keychain.
type KeychainStore struct{}

// Get retrieves a password from the macOS Keychain.
func (k *KeychainStore) Get(service, account string) (string, error) {
	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(service)
	query.SetAccount(account)
	query.SetMatchLimit(keychain.MatchLimitOne)
	query.SetReturnData(true)

	results, err := keychain.QueryItem(query)
	if err != nil {
		if errors.Is(err, keychain.ErrorItemNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}

	if len(results) == 0 {
		return "", ErrNotFound
	}

	return string(results[0].Data), nil
}

// Set stores a password in the macOS Keychain.
// If the credential already exists, it is updated.
func (k *KeychainStore) Set(service, account, password string) error {
	// First, try to delete any existing item
	_ = k.Delete(service, account) // Ignore ErrNotFound

	// Create a new keychain item
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)
	item.SetLabel(service + " - " + account)
	item.SetData([]byte(password))
	item.SetSynchronizable(keychain.SynchronizableNo)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)

	err := keychain.AddItem(item)
	if errors.Is(err, keychain.ErrorDuplicateItem) {
		// Item already exists, try to update it using UpdateItem
		return k.updateItem(service, account, password)
	}
	return err
}

// updateItem updates an existing keychain item.
func (k *KeychainStore) updateItem(service, account, password string) error {
	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(service)
	query.SetAccount(account)

	update := keychain.NewItem()
	update.SetData([]byte(password))

	return keychain.UpdateItem(query, update)
}

// Delete removes a credential from the macOS Keychain.
func (k *KeychainStore) Delete(service, account string) error {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)

	err := keychain.DeleteItem(item)
	if errors.Is(err, keychain.ErrorItemNotFound) {
		return ErrNotFound
	}
	return err
}

// IsSupported returns true for KeychainStore on macOS.
func (k *KeychainStore) IsSupported() bool {
	return true
}
