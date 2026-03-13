package secret

// Linux keychain support via libsecret (secret-tool) is planned for v0.1.1.
// For now, fall back to file-based credentials.

func keychainAvailable() bool {
	return false
}

func keychainGet(name string) (string, error) {
	return "", nil
}

func keychainSet(name, value string) error {
	return fileSet(name, value)
}
