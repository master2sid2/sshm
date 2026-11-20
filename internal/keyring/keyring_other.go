//go:build !linux

package keyring

// Save is a no-op stub on platforms without a session keyring.
func Save(string, []byte) error { return ErrUnsupported }

// Load is a no-op stub on platforms without a session keyring.
func Load(string) ([]byte, error) { return nil, ErrUnsupported }

// Delete is a no-op stub on platforms without a session keyring.
func Delete(string) error { return ErrUnsupported }
