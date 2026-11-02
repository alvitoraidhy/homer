// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sipcapture/homer-core/src/config"
)

func TestJWTSecretConfiguredLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	dsn := "postgres://homer:placeholder@127.0.0.1/homer_settings"
	anchor := config.LocalSecretAnchor(dsn, dir)
	secret, _, path, err := ResolveCoordinatorJWTSecret("", anchor)
	if err != nil || path != filepath.Join(dir, ".homer_jwt_secret") {
		t.Fatalf("JWT path: %q %v", path, err)
	}
	second, generated, _, err := ResolveCoordinatorJWTSecret("", anchor)
	if err != nil || generated || second != secret {
		t.Fatal("JWT secret did not persist")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("JWT permissions: %v", err)
	}
}
