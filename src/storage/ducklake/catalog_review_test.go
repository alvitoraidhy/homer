// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package ducklake

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
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
