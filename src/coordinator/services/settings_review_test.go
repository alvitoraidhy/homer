// Copyright (C) 2026 Homer Server Contributors
// SPDX-License-Identifier: AGPL-3.0-or-later

package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sipcapture/homer-core/src/config"
)

func TestSettingsLiteralRoundTrip(t *testing.T) {
	literal := `homer\path'; SELECT 42; -- café`
	long := strings.Repeat("é", 999) + "界"
	cases := []struct {
		name, input, want, wantUpdated string
	}{
		{"literal", literal, literal, literal + " updated"},
		{"controls", "homer\x00\x01\x08\x0b\x0c\x1f\t\n\r café", "homer\t\n\r café", "homer\t\n\r café updated"},
		{"long_unicode", long + "tail", long, long},
	}
	for _, backend := range []string{"local", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *sql.DB
			if backend == "postgres" {
				db = openPostgresSettingsForTest(t)
			} else {
				var err error
				db, err = OpenSettingsDB(filepath.Join(t.TempDir(), "settings.duckdb"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if err = EnsureSettingsSchema(db); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			payload := json.RawMessage(`{"path":"C:\\homer\\data","line":"a\nb","quote":"'","unicode":"\u00e9","large":"` + strings.Repeat("x", 1500) + `"}`)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					value, updated := tc.input, tc.input+" updated"
					t.Run("users", func(t *testing.T) {
						u := NewUserService(db)
						id, err := u.CreateUser(ctx, value, "placeholder-password", value, value, false, true)
						if err != nil {
							t.Fatal(err)
						}
						got, err := u.GetUserByUsername(ctx, value)
						if err != nil || got == nil || got.Username != tc.want || got.Name != tc.want || got.Email != tc.want {
							t.Fatalf("user roundtrip: %+v %v", got, err)
						}
						byEmail, err := u.GetUserByEmail(ctx, value)
						if err != nil || byEmail == nil || byEmail.ID != id {
							t.Fatalf("email lookup: %+v %v", byEmail, err)
						}
						if ok, err := u.UpdateUser(ctx, id, &updated, nil, &updated, nil, nil); err != nil || !ok {
							t.Fatalf("update: %v", err)
						}
						got, err = u.GetUserByID(ctx, id)
						if err != nil || got == nil || got.Name != tc.wantUpdated || got.Email != tc.wantUpdated {
							t.Fatalf("user update roundtrip: %+v %v", got, err)
						}
						if ok, err := u.DeleteUser(ctx, id); err != nil || !ok {
							t.Fatalf("user delete: %v", err)
						}
					})
					t.Run("global", func(t *testing.T) {
						global := NewGlobalSettingsService(db)
						item := GlobalSetting{GUID: value, Category: value, Param: value, Data: payload}
						if _, err := global.Create(ctx, item); err != nil {
							t.Fatal(err)
						}
						g, err := global.GetByGUID(ctx, value)
						if err != nil || g == nil || g.GUID != tc.want || g.Category != tc.want || g.Param != tc.want || !settingsJSONEqual(g.Data, payload) {
							t.Fatalf("global roundtrip: %+v %v", g, err)
						}
						item.Param = updated
						// Blobs retain their separate NUL-only, untruncated rule.
						item.Data = append(append(json.RawMessage{}, payload...), 0)
						if _, err = global.Update(ctx, value, item); err != nil {
							t.Fatal(err)
						}
						g, err = global.GetByGUID(ctx, value)
						if err != nil || g == nil || g.Category != tc.want || g.Param != tc.wantUpdated || !settingsJSONEqual(g.Data, payload) {
							t.Fatalf("global update roundtrip: %+v %v", g, err)
						}
						if ok, err := global.Delete(ctx, value); err != nil || !ok {
							t.Fatalf("global delete: %v", err)
						}
					})
					t.Run("preferences", func(t *testing.T) {
						prefs := NewUserSettingsService(db)
						for i := 0; i < 2; i++ {
							if _, err := prefs.UpsertByCategory(ctx, value, value, value, payload, 10); err != nil {
								t.Fatal(err)
							}
						}
						pref, err := prefs.GetByCategoryAndParam(ctx, value, value, value)
						if err != nil || pref == nil || pref.UserName != tc.want || pref.Category != tc.want || pref.Param != tc.want || !settingsJSONEqual(pref.Data, payload) {
							t.Fatalf("preference roundtrip: %+v %v", pref, err)
						}
						byUser, err := prefs.ListByUser(ctx, value)
						if err != nil || len(byUser) != 1 {
							t.Fatalf("preferences by user: count=%d err=%v", len(byUser), err)
						}
						byCategory, err := prefs.ListByCategory(ctx, value, value)
						if err != nil || len(byCategory) != 1 {
							t.Fatalf("preferences by category: count=%d err=%v", len(byCategory), err)
						}
						if ok, err := prefs.DeleteByCategoryAndParam(ctx, value, value, value); err != nil || !ok {
							t.Fatalf("preference delete: %v", err)
						}
						if _, err := prefs.UpsertByCategory(ctx, value, value, value, payload, 10); err != nil {
							t.Fatal(err)
						}
						if ok, err := prefs.DeleteByCategory(ctx, value, value); err != nil || !ok {
							t.Fatalf("category delete: %v", err)
						}
					})
					t.Run("mappings", func(t *testing.T) {
						mapping := NewUserMappingService(db)
						for i := 0; i < 2; i++ {
							if _, err := mapping.Upsert(ctx, value, value, payload, 10); err != nil {
								t.Fatal(err)
							}
						}
						mapped, err := mapping.Get(ctx, value, value)
						if err != nil || mapped == nil || mapped.UserName != tc.want || mapped.Param != tc.want || !settingsJSONEqual(mapped.Data, payload) {
							t.Fatalf("mapping roundtrip: %+v %v", mapped, err)
						}
						byUser, err := mapping.ListForUser(ctx, value)
						if err != nil || len(byUser) != 1 {
							t.Fatalf("mappings by user: count=%d err=%v", len(byUser), err)
						}
						if ok, err := mapping.Delete(ctx, value, value); err != nil || !ok {
							t.Fatalf("mapping delete: %v", err)
						}
					})
					t.Run("scripts", func(t *testing.T) {
						scripts := NewScriptsService(db)
						script := HepScript{GUID: value, Profile: value, Type: value, HepAlias: value,
							Script: "-- " + value + "\n" + strings.Repeat("-- \\n\n", 300) + "\x00\x01"}
						if _, err := scripts.Create(ctx, script); err != nil {
							t.Fatal(err)
						}
						saved, err := scripts.GetByGUID(ctx, value)
						if err != nil || saved == nil || saved.GUID != tc.want || saved.Profile != tc.want || saved.HepAlias != tc.want || saved.Type != tc.want || saved.Script != strings.ReplaceAll(script.Script, "\x00", "") {
							t.Fatalf("script roundtrip: %+v %v", saved, err)
						}
						script.Profile, script.HepAlias, script.Type = updated, updated, updated
						script.Script += "return true"
						if _, err = scripts.Update(ctx, value, script); err != nil {
							t.Fatal(err)
						}
						saved, err = scripts.GetByGUID(ctx, value)
						if err != nil || saved == nil || saved.Profile != tc.wantUpdated || saved.HepAlias != tc.wantUpdated || saved.Type != tc.wantUpdated || saved.Script != strings.ReplaceAll(script.Script, "\x00", "") {
							t.Fatalf("script update roundtrip: %+v %v", saved, err)
						}
						if ok, err := scripts.Delete(ctx, value); err != nil || !ok {
							t.Fatalf("script delete: %v", err)
						}
					})
					t.Run("dashboards", func(t *testing.T) {
						dashboards := NewDashboardService(db, nil)
						if _, err := dashboards.CreateDashboard(ctx, value, value, payload); err != nil {
							t.Fatal(err)
						}
						dashboard, err := dashboards.GetDashboard(ctx, value, value)
						if err != nil || dashboard == nil || dashboard.UserName != tc.want || dashboard.Param != tc.want || !settingsJSONEqual(dashboard.Data, payload) {
							t.Fatalf("dashboard roundtrip: %+v %v", dashboard, err)
						}
						listed, err := dashboards.ListDashboards(ctx, value)
						if err != nil || len(listed) != 1 {
							t.Fatalf("dashboard list: count=%d err=%v", len(listed), err)
						}
						if _, err = dashboards.UpdateDashboard(ctx, value, value, payload, false); err != nil {
							t.Fatal(err)
						}
						if ok, err := dashboards.DeleteDashboard(ctx, value, value); err != nil || !ok {
							t.Fatalf("dashboard delete: %v", err)
						}
					})
				})
			}
		})
	}
}

func TestPostgresSettingsDDLQualification(t *testing.T) {
	db := openPostgresSettingsForTest(t)
	// Move both remote and DuckDB search paths away from public, then repeat
	// schema initialization. All DDL, including sequence defaults and ALTERs,
	// must still refer to public objects.
	if _, err := db.Exec("CALL postgres_execute('homer_settings', 'SET search_path TO pg_catalog')"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSettingsSchema(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := postgresQueryRow(db)(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('users','alias','correlation_scripts')").Scan(&count); err != nil || count != 3 {
		t.Fatalf("public schema: count=%d err=%v", count, err)
	}
}

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

func settingsJSONEqual(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
