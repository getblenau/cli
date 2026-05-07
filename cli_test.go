package main

import "testing"

// TestStdoutEncoding will assert that stdout is UTF-8 NFC, no BOM, on every
// supported platform once setUTF8Stdout() is fully implemented.
func TestStdoutEncoding(t *testing.T) {
	t.Skip("placeholder — implement when setUTF8Stdout() is wired up")
}
