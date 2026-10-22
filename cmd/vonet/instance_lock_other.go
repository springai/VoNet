//go:build !linux

package main

import (
	"os"
	"path/filepath"
)

func lockServerInstance(databasePath string) (*os.File, error) {
	return os.OpenFile(filepath.Join(filepath.Dir(databasePath), ".vonet.lock"), os.O_CREATE|os.O_RDWR, 0o600)
}
