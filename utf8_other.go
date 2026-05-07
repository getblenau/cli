//go:build !windows

package main

// setUTF8Stdout is a no-op on non-Windows platforms (stdout is already UTF-8).
func setUTF8Stdout() {}
