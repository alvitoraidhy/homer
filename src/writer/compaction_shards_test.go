package writer

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sipcapture/homer-core/src/config"
	"github.com/sipcapture/homer-core/src/storage/ducklake"
)

func TestPostgresCompactionAcrossShards(t *testing.T) {
	dsn := os.Getenv("HOMER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set HOMER_TEST_POSTGRES_DSN for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, config.NormalizePostgresDSN(dsn))
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("homer_compaction_shards_%d", time.Now().UnixNano())
	created := []string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range created {
			_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		}
		_ = admin.Close(ctx)
	})
	for _, name := range []string{base + "_0", base + "_1"} {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		created = append(created, name)
	}
	pg, err := pgx.ParseConfig(config.NormalizePostgresDSN(dsn))
	if err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "postgresql", Host: net.JoinHostPort(pg.Host, strconv.Itoa(int(pg.Port))), User: url.UserPassword(pg.User, pg.Password), Path: "/" + base, RawQuery: "sslmode=disable"}
	dir := t.TempDir()
	storage := config.DuckLakeConfig{CatalogType: "postgres", CatalogPath: u.String(), LakeName: "lake", DataPath: filepath.Join(dir, "data"), ShardCount: 2,
		Compaction: config.CompactionConfig{Enable: true, CheckIntervalSec: 3600, RetentionDays: 7, SnapshotExpireIntervalSec: 1, MemoryLimit: "256MB"}}
	mgr, err := ducklake.NewManager(ducklake.Config{CatalogType: ducklake.CatalogPostgres, CatalogPath: storage.CatalogPath, LakeName: storage.LakeName, DataPath: storage.DataPath, ShardCount: 2,
		TuningThreads: 1, TuningMemoryLimit: "256MB", TuningTempDirectory: filepath.Join(dir, "spill")})
	if err != nil {
		t.Fatal(err)
	}
	w := &Writer{storageConfig: &config.StorageConfig{DuckLake: storage}, ducklakeManager: mgr}
	defer w.Stop()
	shards := mgr.GetShards()
	for _, shard := range shards {
		table := shard.GetLakeName() + ".main.otlp_shard_test"
		for _, stmt := range []string{
			"CREATE TABLE " + table + " (date DATE, timestamp TIMESTAMP, value INTEGER)",
			"INSERT INTO " + table + " VALUES (CURRENT_DATE - 10, CURRENT_TIMESTAMP - INTERVAL '10 days', 1)",
			"INSERT INTO " + table + " VALUES (CURRENT_DATE, CURRENT_TIMESTAMP, 2)",
		} {
			if _, err := shard.GetDB().Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An object unknown to either catalog must not be swept from the shared path.
	foreign := filepath.Join(storage.DataPath, "another-catalog.parquet")
	if err := os.WriteFile(foreign, []byte("belongs to another catalog"), 0600); err != nil {
		t.Fatal(err)
	}
	holder, acquired, err := ducklake.AcquireMaintenance(ctx, ducklake.CatalogPostgres, shards[1].GetCatalogPath())
	if err != nil || !acquired {
		t.Fatalf("second shard lock: %v %v", acquired, err)
	}
	defer holder.Release()
	if err := w.startCompactionService(); err != nil {
		t.Fatal(err)
	}
	if len(w.compactionServices) != 2 {
		t.Fatalf("started %d services, want 2", len(w.compactionServices))
	}
	for i, service := range w.compactionServices {
		if service.lakeName != shards[i].GetLakeName() || service.catalogPath != shards[i].GetCatalogPath() || service.catalogLocker != shards[i] {
			t.Fatalf("service %d is attached to the wrong shard", i)
		}
	}
	// Startup maintenance on shard 0 must proceed while shard 1 is locked by
	// another replica. Each shard's lock is independent.
	deadline := time.After(25 * time.Second)
	for w.compactionServices[0].GetStats()["total_compactions"].(int64) == 0 {
		select {
		case <-deadline:
			t.Fatal("automatic maintenance did not run on the first shard")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if got := w.compactionServices[1].GetStats()["total_compactions"].(int64); got != 0 {
		t.Fatalf("locked second shard ran %d cycles", got)
	}
	holder.Release()
	w.compactionServices[1].runCompaction()
	for i, service := range w.compactionServices {
		var count, value int
		query := "SELECT count(*),max(value) FROM " + service.lakeName + ".main.otlp_shard_test"
		if err := service.database().QueryRow(query).Scan(&count, &value); err != nil || count != 1 || value != 2 {
			t.Fatalf("shard %d retention: count=%d value=%d err=%v", i, count, value, err)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("orphan cleanup touched another catalog's object: %v", err)
	}
	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
	for i, service := range w.compactionServices {
		select {
		case <-service.ctx.Done():
		default:
			t.Fatalf("shard %d maintenance was not stopped", i)
		}
	}
}
