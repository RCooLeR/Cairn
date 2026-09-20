//go:build server && !cairn_server_dev

// Package main intentionally has no main function for a plain Wails server
// build. Cairn's bound services have desktop authority and are not a remotely
// safe API. Development-only server builds must also specify the
// cairn_server_dev tag and satisfy the runtime acknowledgement guard.
package main
