# Configure PostgreSQL-backed settings

This guide explains how to store Homer coordinator settings in PostgreSQL.
DuckDB remains the query client; a local DuckDB file is the default settings store.

Settings storage is separate from DuckLake metadata. To store lake catalogs in
PostgreSQL, follow [Configure PostgreSQL DuckLake catalogs](POSTGRES_DUCKLAKE.md).
Use a separate PostgreSQL database for each purpose.

## Before you begin

You need:

- An existing PostgreSQL database and a role that can create settings tables in its `public` schema.
- Network access to PostgreSQL from the coordinator.
- The DuckDB PostgreSQL extension installed for the DuckDB engine and OS user that run Homer.

Switching the configuration does not migrate existing local settings. Homer does
not open the previous settings file while PostgreSQL is configured.

## 1. Install the extension

Run this command as the OS user that runs Homer:

```sh
homer-core system --install-extensions
```

The command includes the `postgres` extension.

## 2. Configure the coordinator

Set `coordinator.settings_db_path` in your Homer configuration. Replace the host,
credentials, and database name with your PostgreSQL connection details:

```json
{
  "coordinator": {
    "local_secrets_dir": "/var/lib/homer",
    "settings_db_path": "postgres://homer:placeholder@127.0.0.1:5432/homer_settings?sslmode=require"
  }
}
```

Alternatively, set the environment variable:

```sh
export HOMER_COORDINATOR_SETTINGS_DB_PATH='postgres://homer:placeholder@127.0.0.1:5432/homer_settings?sslmode=require'
```

Homer recognizes both `postgres://` and `postgresql://` URLs. Libpq keyword
strings also work, for example:

```text
host=127.0.0.1 dbname=homer_settings user=homer
```

Recognized connection strings are validated before use. A malformed connection
string fails instead of opening a local file.

To use local DuckDB storage, set a file path such as
`/var/lib/homer/homer_settings.duckdb`.

## 3. Start Homer and verify settings

Start or restart the coordinator with the updated configuration. Homer creates
the settings tables in the PostgreSQL database's `public` schema.

Use the coordinator UI to create or update a setting, then reload it to verify
that it can be read back. Restart the coordinator and confirm the setting is
still present.

## Manage settings and local secrets

The administrator password reset command uses the configured settings backend.
The existing migration command requires a local DuckDB destination; when your
coordinator uses PostgreSQL, supply a local file with `--duckdb`.

Set `coordinator.local_secrets_dir` to a persistent directory writable by the
Homer process. PostgreSQL defaults to `/var/lib/homer`; generated JWT and
FlightSQL authentication files are written there with mode `0600`. A node using
a PostgreSQL catalog has the same default: set `node.local_secrets_dir` to the
same directory when running together. Environment overrides are
`HOMER_COORDINATOR_LOCAL_SECRETS_DIR` and `HOMER_NODE_LOCAL_SECRETS_DIR`.
With file-backed settings, secrets remain beside the settings file unless the
new field is explicitly set. See the
[configuration reference](ENVIRONMENT_VARIABLES.md#local-authentication-secrets).

Config display redacts the connection string unless you supply
`--include-secrets`, including when selecting it directly with `--section`.
Connection strings are not logged as file paths.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| Settings initialization fails | Confirm that the database already exists, the credentials work, and the role can create tables in `public`. |
| PostgreSQL extension cannot load | Install extensions using the same DuckDB engine and OS user that run Homer. |
| Existing settings are missing after switching | Local settings are not migrated automatically. Check which backend is configured. |
| Local authentication secrets cannot be saved | Check permissions and persistence for `coordinator.local_secrets_dir`. |
| Migration command rejects PostgreSQL | Supply a local DuckDB destination with `--duckdb`. |

## Query behavior

Most settings queries run through DuckDB's PostgreSQL attachment. Operations that
need `RETURNING` use a native PostgreSQL connection for that query. Each native
query has its own connection and transaction, adding authentication and network
overhead.

Values are bound as parameters, preserving backslashes, quotes and full JSON/Lua
content on both backends. Identity/text fields retain their existing validation:
truncate to 1,000 runes first, then strip NUL and control characters below U+0020
except tab, newline and carriage return. JSON/Lua blobs strip NUL without
truncation. Schema DDL is sent one statement at a time with
`public`-qualified table and sequence names. Metadata refresh errors fail startup.

These queries cannot join a DuckDB transaction. Existing settings services do not
use explicit multi-statement transactions. Writes commit before subsequent
DuckDB autocommit reads; an already-open DuckDB transaction may retain an earlier
snapshot. As with other database writes, an error after a write reaches the
server can leave its commit outcome uncertain.

## Compatibility

For identity/text fields, the only remaining local DuckDB storage difference
from earlier versions is that bound values no longer double backslashes. DuckDB
does not treat backslashes as escapes in ordinary single-quoted literals, so
earlier versions stored them doubled. Existing rows whose username, category or
param contained a backslash therefore will not match a lookup by the original
value. The strip and truncation rules remain unchanged; no migration is included.

## Run regression tests

These commands are for development verification. The integration test creates
and drops its own database, so use a disposable server and a test role with
`CREATE DATABASE` privileges.

Run local regression tests from `src`:

```sh
go test ./config ./coordinator/services ./coordinator/handlers ./coordinator ./cli
```

Install the matching DuckDB extensions using the checksum-verified bundle first
(from the repository root, for the test platform):

```sh
./scripts/download_duckdb_extensions.sh
mkdir -p "$HOME/.duckdb/extensions"
cp -R bundled_extensions/v1.5.6 "$HOME/.duckdb/extensions/"
```

For integration tests, start a disposable PostgreSQL server:

```sh
docker run --detach --name homerpg-settings-probe \
  --memory 6g --publish 127.0.0.1:55432:5432 \
  --env POSTGRES_USER=homer --env POSTGRES_PASSWORD=placeholder \
  --env POSTGRES_DB=homer_probe postgres:18

docker exec homerpg-settings-probe pg_isready -U homer -d homer_probe

# Run from src; the test role needs CREATE DATABASE privileges.
HOMER_TEST_POSTGRES_DSN='postgres://homer:placeholder@127.0.0.1:55432/homer_probe?sslmode=disable' \
  go test ./coordinator/services -run 'TestPostgresSettingsServices' -count=1 -v

docker rm --force --volumes homerpg-settings-probe
```

The integration test covers schema initialization, settings CRUD,
read-after-write visibility, JSON and Lua content, missing rows, pooled sessions,
cancellation, connection cleanup, and concurrent token consumption.

## Further reading

- [Configure PostgreSQL DuckLake catalogs](POSTGRES_DUCKLAKE.md)
- [DuckDB PostgreSQL extension](https://duckdb.org/docs/stable/extensions/postgres)
- [Upstream RETURNING limitation](https://github.com/duckdb/duckdb-postgres/issues/175)
