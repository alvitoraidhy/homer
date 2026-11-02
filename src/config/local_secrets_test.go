// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPostgresLocalSecretsDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMER_NODE_LOCAL_SECRETS_DIR", dir)
	t.Setenv("HOMER_COORDINATOR_LOCAL_SECRETS_DIR", dir)
	file := filepath.Join(t.TempDir(), "homer.json")
	data := `{"node":{"enable":true,"ducklake":{"catalog_path":"postgres://homer:placeholder@127.0.0.1/homer"}},"coordinator":{"enable":true,"settings_db_path":"postgres://homer:placeholder@127.0.0.1/homer_settings","flightsql_server":{"enable":true}}}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.FlightServer.AuthToken == "" || cfg.Coordinator.FlightSQLServer.AuthToken == "" {
		t.Fatal("token was not generated")
	}
	path := filepath.Join(dir, ".homer_node_auth_token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token mode: %v", info.Mode())
	}
	cfg2, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.FlightServer.AuthToken != cfg2.Node.FlightServer.AuthToken {
		t.Fatal("token did not persist")
	}
	for _, dsn := range []string{"postgres://homer:placeholder@127.0.0.1/homer", "host=127.0.0.1 dbname=homer"} {
		if NodeAuthTokenFilePath(dsn) != "/var/lib/homer/.homer_node_auth_token" {
			t.Fatal("PostgreSQL secrets have inconsistent default directories")
		}
	}
	if NodeAuthTokenFilePath("") != "/data/.homer_node_auth_token" || NodeAuthTokenFilePath("/tmp/homer/catalog.sqlite") != "/tmp/homer/.homer_node_auth_token" {
		t.Fatal("local defaults changed")
	}
}

func TestPostgresVolumeSecretsDirectory(t *testing.T) {
	cfg := &NodeConfig{DuckLake: DuckLakeConfig{CatalogPath: "homer_catalog.sqlite", Volumes: []VolumeConfig{{CatalogPath: "postgres://homer:placeholder@127.0.0.1/homer"}}}}
	if got := NodeAuthTokenFilePath(NodeSecretAnchor(cfg)); got != "/var/lib/homer/.homer_node_auth_token" {
		t.Fatalf("volume secret path: %s", got)
	}
	cfg.LocalSecretsDir = t.TempDir()
	token, _, path, err := ResolveRequiredAuthToken("", NodeSecretAnchor(cfg))
	if err != nil || token == "" || filepath.Dir(path) != cfg.LocalSecretsDir {
		t.Fatalf("volume secret persistence: %s %v", path, err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.LocalSecretsDir = blocked
	if _, _, _, err := ResolveRequiredAuthToken("", NodeSecretAnchor(cfg)); err == nil {
		t.Fatal("unwritable secret directory accepted")
	}
}
