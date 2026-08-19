//go:build windows

package main

// checkNotRoot is a no-op on Windows: there is no euid, and the unix ownership
// trap it guards against (root-created files a normal user cannot read) has no
// direct equivalent.
func checkNotRoot(_ ...string) error { return nil }
