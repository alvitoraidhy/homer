// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// NormalizePostgresDSN accepts URL schemes case-insensitively, like detection.
// pgx and libpq require the lowercase spelling. Keyword DSNs are unchanged.
func NormalizePostgresDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if scheme, rest, ok := strings.Cut(dsn, "://"); ok &&
		(strings.EqualFold(scheme, "postgres") || strings.EqualFold(scheme, "postgresql")) {
		return strings.ToLower(scheme) + "://" + rest
	}
	return dsn
}

// PostgresDisplayPath retains the endpoint and database, never credentials or
// connection options (which can themselves contain passwords).
func PostgresDisplayPath(dsn string) string {
	cfg, err := pgx.ParseConfig(NormalizePostgresDSN(dsn))
	if err != nil {
		return "PostgreSQL (invalid connection string)"
	}
	return fmt.Sprintf("PostgreSQL host=%q port=%d dbname=%q", cfg.Host, cfg.Port, cfg.Database)
}

// PostgresError retains server diagnostics while removing connection strings
// echoed by DuckDB and password spellings used by libpq and pgx.
func PostgresError(dsn string, err error) error {
	if err == nil {
		return nil
	}
	normalized := NormalizePostgresDSN(dsn)
	cfg, parseErr := pgx.ParseConfig(normalized)
	if parseErr != nil {
		return fmt.Errorf("invalid PostgreSQL connection string")
	}
	message := err.Error()
	for _, value := range []string{dsn, normalized} {
		message = strings.ReplaceAll(message, strings.ReplaceAll(value, "'", "''"), PostgresDisplayPath(dsn))
		message = strings.ReplaceAll(message, value, PostgresDisplayPath(dsn))
	}
	for _, secret := range []string{cfg.Password, cfg.RuntimeParams["sslpassword"]} {
		if secret == "" {
			continue
		}
		for _, value := range []string{
			strings.ReplaceAll(strings.ReplaceAll(secret, `\`, `\\`), "'", `\'`),
			strings.ReplaceAll(secret, "'", "''"),
			url.QueryEscape(secret), url.PathEscape(secret), secret,
		} {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return fmt.Errorf("%s", message)
}
