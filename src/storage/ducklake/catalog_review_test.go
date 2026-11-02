// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package ducklake

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	logger "github.com/sipcapture/homer-core/src/utils/logging"
)

func TestPostgresCatalogURLSchemes(t *testing.T) {
	for _, dsn := range []string{
		"POSTGRES://homer:placeholder@127.0.0.1/homer?sslmode=disable",
		"postgresql://homer:placeholder@127.0.0.1/homer?sslmode=disable",
		"PostgreSQL://homer:placeholder@127.0.0.1/homer?sslmode=disable",
		"host=127.0.0.1 dbname=homer user=homer password='place holder' sslmode=disable",
	} {
		ct, err := ResolveCatalogType("", dsn)
		if err != nil || ct != CatalogPostgres {
			t.Fatalf("resolve: %s %v", ct, err)
		}
		got, err := suffixedPostgresCatalog(dsn, "cold")
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := pgx.ParseConfig(got)
		if err != nil || cfg.Database != "homer_cold" || cfg.Host != "127.0.0.1" {
			t.Fatalf("derived catalog: %v", err)
		}
		stmt, err := CatalogAttachSQL(ct, dsn, `lake"name`, "/tmp/homer's", false)
		if err != nil || !strings.Contains(stmt, `AS "lake""name"`) || !strings.Contains(stmt, "homer''s") {
			t.Fatalf("quoting: %v", err)
		}
	}
}

func TestCatalogAttachErrorsPreserveDiagnostics(t *testing.T) {
	for _, dsn := range []string{
		"postgres://homer:place%27holder@127.0.0.1/homer_missing?sslmode=disable",
		"host=127.0.0.1 dbname=homer_missing user=homer password='place\\'holder'",
	} {
		cause := errors.New("FATAL: database homer_missing does not exist; ATTACH '" + strings.ReplaceAll(dsn, "'", "''") + "'; password=place'holder")
		got := CatalogError(dsn, "attach", cause).Error()
		for _, want := range []string{"127.0.0.1", "homer_missing", "FATAL", "does not exist"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %s", want, got)
			}
		}
		for _, secret := range []string{"place'holder", "place''holder", "place%27holder", `place\'holder`} {
			if strings.Contains(got, secret) {
				t.Fatal("attach error leaked credentials")
			}
		}
	}
	cause := errors.New("local catalog error")
	if !errors.Is(CatalogError("homer.sqlite", "attach", cause), cause) {
		t.Fatal("local error wrapping changed")
	}
}

func TestAttachBuildersReturnErrors(t *testing.T) {
	bad := "postgres://homer:placeholder@127.0.0.1:%zz/homer"
	if _, err := buildLakeAttachSQL(Config{CatalogPath: bad}); err == nil {
		t.Fatal("writer dropped attach error")
	}
	if _, err := volumeAttachSQL(&Volume{}, bad); err == nil {
		t.Fatal("volume dropped attach error")
	}
}

func TestPostgresSpillWarning(t *testing.T) {
	old := logger.Logger
	defer func() { logger.Logger = old }()
	for _, who := range []string{"writer", "node", "compaction", "tiering", "tiering-maintenance"} {
		t.Run(who, func(t *testing.T) {
			var output bytes.Buffer
			logger.Logger = slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelWarn}))
			db, err := sql.Open("duckdb", "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ApplyHomerDuckDBDefaults(db, 1, "128MB", "", "postgres://homer:placeholder@127.0.0.1/homer", who)
			got := output.String()
			if !strings.Contains(got, "disk spilling is disabled") || !strings.Contains(got, "tuning.temp_directory") || !strings.Contains(got, who) {
				t.Fatalf("missing actionable warning: %s", got)
			}
			if strings.Contains(got, "placeholder") {
				t.Fatal("warning leaked credentials")
			}
			output.Reset()
			ApplyHomerDuckDBDefaults(db, 1, "128MB", t.TempDir(), "postgres://homer:placeholder@127.0.0.1/homer", who)
			if strings.Contains(output.String(), "disk spilling is disabled") {
				t.Fatal("warned despite explicit spill path")
			}
		})
	}
}

func TestSQLiteShardSpillDirectoryUnchanged(t *testing.T) {
	dir := t.TempDir()
	spill := filepath.Join(dir, "spill")
	writer, err := NewShardedWriter(Config{CatalogPath: filepath.Join(dir, "catalog.sqlite"),
		LakeName: "lake", DataPath: filepath.Join(dir, "data"), ShardCount: 2,
		TuningThreads: 1, TuningMemoryLimit: "128MB", TuningTempDirectory: spill})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Stop()
	for _, shard := range writer.Shards() {
		var actual string
		if err := shard.db.QueryRow("SELECT current_setting('temp_directory')").Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != spill {
			t.Fatalf("SQLite shard spill directory changed: %q, want %q", actual, spill)
		}
	}
}
