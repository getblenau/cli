//go:build windows

package main

import "golang.org/x/sys/windows"

// setUTF8Stdout forces the Windows console code page to UTF-8 (65001) so
// non-ASCII characters survive when an agent captures stdout to a file.
func setUTF8Stdout() {
	_ = windows.SetConsoleOutputCP(65001)
}
