#!/usr/bin/env python3
"""Build and verify deterministic authentication restore fixtures for CI."""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import sqlite3
import tempfile
from pathlib import Path


SCHEMA_VERSION = 35
RESTORED_SCHEMA_VERSION = 34
CREATED_AT = 1_700_000_000
EXPIRES_AT = 4_102_444_800
PASSWORD_SESSION_HASH = bytes.fromhex("11" * 32)
SSO_SESSION_HASH = bytes.fromhex("22" * 32)
WAL_SESSION_HASH = b"WAL_ONLY_SESSION_TOKEN_HASH_V1!!"
API_TOKEN_HASH = bytes.fromhex("33" * 32)
STATE_HASH = bytes.fromhex("44" * 32)
BROWSER_TOKEN_HASH = bytes.fromhex("55" * 32)
NONCE_HASH = bytes.fromhex("66" * 32)

SSO_CONFIG = {
    "revision": 7,
    "provider_instance_id": "provider-restore-smoke",
    "enabled": True,
    "kind": "pocketid",
    "display_name": "Restore smoke",
    "public_origin": "https://vonet.example.test",
    "client_id": "vonet-restore-smoke",
    "issuer_url": "https://id.example.test",
    "scopes": ["openid", "profile", "email"],
    "token_auth_method": "client_secret_basic",
    "pkce_mode": "s256",
}
SSO_CONFIG_JSON = json.dumps(SSO_CONFIG, separators=(",", ":"), sort_keys=True)
SSO_SECRET_JSON = json.dumps("legacy-client-secret", separators=(",", ":"))


def fail(message: str) -> None:
    raise SystemExit(f"auth restore smoke: {message}")


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def sqlite_columns(connection: sqlite3.Connection, table: str) -> set[str]:
    return {row[1] for row in connection.execute(f"PRAGMA table_info({table})")}


def sqlite_version(connection: sqlite3.Connection) -> int:
    return int(connection.execute("PRAGMA user_version").fetchone()[0])


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def snapshot_paths(database: Path) -> dict[str, Path]:
    return {
        "database": database,
        "wal": Path(str(database) + "-wal"),
        "shm": Path(str(database) + "-shm"),
    }


def assert_full_schema(path: Path) -> None:
    require(path.is_file(), f"missing full migration database: {path}")
    connection = sqlite3.connect(path)
    try:
        require(
            connection.execute("PRAGMA integrity_check").fetchone()[0] == "ok",
            "full migration database failed integrity_check",
        )
        require(
            not connection.execute("PRAGMA foreign_key_check").fetchall(),
            "full migration database failed foreign_key_check",
        )
        require(
            sqlite_version(connection) == SCHEMA_VERSION,
            f"user_version is not {SCHEMA_VERSION}",
        )
        sessions = sqlite_columns(connection, "sessions")
        require("secure_cookies" in sessions, "migration 35 did not add sessions.secure_cookies")

        milestones = {
            "phone_associations": "migration 3",
            "device_proxy_bindings": "migration 6",
            "api_tokens": "migration 33",
            "sso_bindings": "migration 34",
            "sso_flows": "migration 34",
        }
        tables = {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        }
        for table, source in milestones.items():
            require(table in tables, f"{source} did not create {table}")

        sms_columns = sqlite_columns(connection, "sms_messages")
        device_columns = sqlite_columns(connection, "devices")
        admins_columns = sqlite_columns(connection, "admins")
        tokens_columns = sqlite_columns(connection, "api_tokens")
        require("modem_imei" in sms_columns, "migration 7 did not add sms_messages.modem_imei")
        require("device_type" in device_columns, "migration 8 did not add devices.device_type")
        require("credential_epoch" in admins_columns, "migration 34 did not add admins.credential_epoch")
        require(
            "credential_epoch" in tokens_columns,
            "migration 34 did not add api_tokens.credential_epoch",
        )
        for obsolete in (
            "card_policies_v8",
            "automatic_tasks_v24",
            "automatic_task_runs_v24",
        ):
            require(obsolete not in tables, f"migration left temporary table {obsolete}")
    finally:
        connection.close()


def insert_restore_rows(connection: sqlite3.Connection) -> None:
    connection.execute(
        """
        INSERT INTO admins (
            id, username, password_hash, created_at, updated_at, credential_epoch
        ) VALUES (1, 'restore-admin', ?, ?, ?, 7)
        """,
        (bytes.fromhex("aa" * 32), CREATED_AT, CREATED_AT),
    )
    connection.executemany(
        """
        INSERT INTO app_settings (key, value_json, sensitive, updated_at)
        VALUES (?, ?, ?, ?)
        """,
        (
            ("auth.sso.config", SSO_CONFIG_JSON, 0, CREATED_AT),
            ("auth.sso.client_secret", SSO_SECRET_JSON, 1, CREATED_AT),
        ),
    )
    connection.execute(
        """
        INSERT INTO sso_bindings (
            id, admin_id, provider_instance_id, protocol, authority, subject,
            display_name, email, created_at, updated_at, last_login_at
        ) VALUES (
            'binding-restore-smoke', 1, 'provider-restore-smoke', 'oidc',
            'https://id.example.test', 'stable-restore-subject',
            'Restore User', 'restore@example.test', ?, ?, ?
        )
        """,
        (CREATED_AT, CREATED_AT, CREATED_AT),
    )
    connection.execute(
        """
        INSERT INTO sessions (
            token_hash, admin_id, csrf_hash, expires_at, created_at,
            login_method, sso_binding_id, credential_epoch
        ) VALUES (?, 1, ?, ?, ?, 'password', NULL, 7)
        """,
        (PASSWORD_SESSION_HASH, bytes.fromhex("bb" * 32), EXPIRES_AT, CREATED_AT),
    )
    connection.execute(
        """
        INSERT INTO sessions (
            token_hash, admin_id, csrf_hash, expires_at, created_at,
            login_method, sso_binding_id, credential_epoch
        ) VALUES (?, 1, ?, ?, ?, 'sso', 'binding-restore-smoke', 7)
        """,
        (SSO_SESSION_HASH, bytes.fromhex("cc" * 32), EXPIRES_AT, CREATED_AT),
    )
    connection.execute(
        """
        INSERT INTO api_tokens (
            name, token_hash, read_only, expires_at, created_at,
            last_used_at, credential_epoch
        ) VALUES ('restore-token', ?, 1, ?, ?, 0, 7)
        """,
        (API_TOKEN_HASH, EXPIRES_AT, CREATED_AT),
    )
    connection.execute(
        """
        INSERT INTO sso_flows (
            id, state_hash, browser_token_hash, purpose,
            provider_instance_id, config_revision,
            initiating_session_hash, admin_id, nonce_hash,
            pkce_verifier, redirect_uri, return_path,
            created_at, expires_at, claimed_at
        ) VALUES (
            'flow-restore-smoke', ?, ?, 'login',
            'provider-restore-smoke', 7,
            NULL, NULL, ?,
            'restore-verifier', 'https://vonet.example.test/api/auth/sso/callback',
            '/settings', ?, ?, 0
        )
        """,
        (STATE_HASH, BROWSER_TOKEN_HASH, NONCE_HASH, CREATED_AT, EXPIRES_AT),
    )


def prepare_fixture(source: Path, output: Path) -> None:
    assert_full_schema(source)
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        fail(f"output database already exists: {output}")

    with tempfile.TemporaryDirectory(prefix="vonet-auth-fixture-") as temporary:
        working = Path(temporary) / "vonet.db"
        shutil.copy2(source, working)
        connection = sqlite3.connect(working)
        try:
            connection.execute("PRAGMA foreign_keys = ON")
            connection.execute("PRAGMA journal_mode = WAL")
            require("secure_cookies" in sqlite_columns(connection, "sessions"), "source is not schema 35")
            connection.execute("ALTER TABLE sessions DROP COLUMN secure_cookies")
            connection.execute(
                f"PRAGMA user_version = {RESTORED_SCHEMA_VERSION}"
            )
            for statement in (
                "DELETE FROM sso_flows",
                "DELETE FROM sessions",
                "DELETE FROM api_tokens",
                "DELETE FROM sso_bindings",
                "DELETE FROM app_settings WHERE key LIKE 'auth.sso.%'",
                "DELETE FROM admins",
            ):
                connection.execute(statement)
            insert_restore_rows(connection)
            connection.commit()
            checkpoint = connection.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()
            require(checkpoint is not None and checkpoint[0] == 0, "could not checkpoint fixture baseline")

            # This row is deliberately committed after the checkpoint so it is
            # recoverable only from the accompanying WAL/SHM snapshot.
            connection.execute(
                """
                INSERT INTO sessions (
                    token_hash, admin_id, csrf_hash, expires_at, created_at,
                    login_method, sso_binding_id, credential_epoch
                ) VALUES (?, 1, ?, ?, ?, 'sso', 'binding-restore-smoke', 7)
                """,
                (WAL_SESSION_HASH, bytes.fromhex("dd" * 32), EXPIRES_AT, CREATED_AT),
            )
            connection.commit()

            paths = snapshot_paths(working)
            require(paths["wal"].is_file(), "fixture writer did not create SQLite WAL")
            require(paths["shm"].is_file(), "fixture writer did not create SQLite SHM")
            output.parent.mkdir(parents=True, exist_ok=True)
            destinations = {
                "database": output,
                "wal": Path(str(output) + "-wal"),
                "shm": Path(str(output) + "-shm"),
            }
            for name, path in paths.items():
                shutil.copy2(path, destinations[name])

            # SQLite hot snapshots are a set. Verify the WAL boundary while the
            # writer is still open, then verify that the main file alone omits
            # the post-checkpoint row.
            with tempfile.TemporaryDirectory(prefix="vonet-auth-boundary-") as boundary:
                boundary_root = Path(boundary)
                full_copy = boundary_root / "full"
                main_copy = boundary_root / "main-only"
                full_copy.mkdir()
                main_copy.mkdir()
                for name, path in snapshot_paths(output).items():
                    shutil.copy2(path, full_copy / path.name)
                shutil.copy2(output, main_copy / output.name)

                full_connection = sqlite3.connect(full_copy / output.name)
                try:
                    full_rows = {
                        row[0] for row in full_connection.execute("SELECT hex(token_hash) FROM sessions")
                    }
                finally:
                    full_connection.close()
                main_connection = sqlite3.connect(main_copy / output.name)
                try:
                    main_rows = {
                        row[0] for row in main_connection.execute("SELECT hex(token_hash) FROM sessions")
                    }
                finally:
                    main_connection.close()
                wal_marker = WAL_SESSION_HASH.hex().upper()
                require(wal_marker in full_rows, "WAL-only session is missing from the complete snapshot")
                require(wal_marker not in main_rows, "WAL-only session leaked into the checkpointed main database")

            manifest = {
                "schema_version": RESTORED_SCHEMA_VERSION,
                "journal_mode": "wal",
                "wal_only_session": WAL_SESSION_HASH.hex(),
                "wal_only_session_in_main": False,
                "files": {
                    name: {"name": path.name, "bytes": path.stat().st_size, "sha256": sha256(path)}
                    for name, path in snapshot_paths(output).items()
                },
            }
            output.with_name(output.name + ".snapshot.json").write_text(
                json.dumps(manifest, indent=2, sort_keys=True) + "\n",
                encoding="ascii",
            )
        finally:
            connection.close()


def copy_snapshot_for_read(database: Path, destination: Path) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    for path in snapshot_paths(database).values():
        if path.exists():
            shutil.copy2(path, destination / path.name)


def assert_schema34_snapshot(database: Path) -> None:
    manifest_path = database.with_name(database.name + ".snapshot.json")
    require(manifest_path.is_file(), f"missing snapshot manifest: {manifest_path}")
    manifest = json.loads(manifest_path.read_text(encoding="ascii"))
    require(manifest["schema_version"] == RESTORED_SCHEMA_VERSION, "snapshot manifest schema is not 34")
    require(manifest["journal_mode"] == "wal", "snapshot manifest journal mode is not WAL")
    require(manifest["wal_only_session_in_main"] is False, "manifest does not record WAL boundary")
    paths = snapshot_paths(database)
    for name, path in paths.items():
        require(path.is_file(), f"snapshot is missing {name} file: {path}")
        expected = manifest["files"][name]
        require(path.name == expected["name"], f"snapshot {name} filename changed")
        require(path.stat().st_size == expected["bytes"], f"snapshot {name} size changed")
        require(sha256(path) == expected["sha256"], f"snapshot {name} hash changed")
    require(
        WAL_SESSION_HASH not in database.read_bytes(),
        "WAL-only marker unexpectedly appears in the main database",
    )

    with tempfile.TemporaryDirectory(prefix="vonet-auth-inspect-") as temporary:
        root = Path(temporary)
        full_copy = root / "full"
        main_copy = root / "main-only"
        copy_snapshot_for_read(database, full_copy)
        main_copy.mkdir()
        shutil.copy2(database, main_copy / database.name)

        connection = sqlite3.connect(full_copy / database.name)
        try:
            require(sqlite_version(connection) == RESTORED_SCHEMA_VERSION, "restore fixture is not schema 34")
            require(
                "secure_cookies" not in sqlite_columns(connection, "sessions"),
                "restore fixture unexpectedly contains migration 35 session column",
            )
            require(
                connection.execute("PRAGMA journal_mode").fetchone()[0] == "wal",
                "restore fixture is not in WAL mode",
            )
            session_rows = {
                bytes.fromhex(row[0])
                for row in connection.execute("SELECT hex(token_hash) FROM sessions")
            }
            require(
                session_rows == {PASSWORD_SESSION_HASH, SSO_SESSION_HASH, WAL_SESSION_HASH},
                "complete restore fixture lost a schema 34 session",
            )
            assert_restore_rows(connection)
        finally:
            connection.close()

        connection = sqlite3.connect(main_copy / database.name)
        try:
            main_rows = {
                bytes.fromhex(row[0])
                for row in connection.execute("SELECT hex(token_hash) FROM sessions")
            }
        finally:
            connection.close()
        require(
            main_rows == {PASSWORD_SESSION_HASH, SSO_SESSION_HASH},
            "main-only restore boundary did not omit the WAL-only session",
        )


def assert_restore_rows(connection: sqlite3.Connection) -> None:
    token = connection.execute(
        "SELECT name, read_only, expires_at, created_at, last_used_at, credential_epoch FROM api_tokens WHERE token_hash = ?",
        (API_TOKEN_HASH,),
    ).fetchone()
    require(token == ("restore-token", 1, EXPIRES_AT, CREATED_AT, 0, 7), "legacy API token changed")
    assert_sso_restore_rows(connection)


def assert_sso_restore_rows(connection: sqlite3.Connection) -> None:
    config = connection.execute(
        "SELECT value_json, sensitive, updated_at FROM app_settings WHERE key = 'auth.sso.config'"
    ).fetchone()
    secret = connection.execute(
        "SELECT value_json, sensitive, updated_at FROM app_settings WHERE key = 'auth.sso.client_secret'"
    ).fetchone()
    require(config == (SSO_CONFIG_JSON, 0, CREATED_AT), "SSO configuration changed")
    require(secret == (SSO_SECRET_JSON, 1, CREATED_AT), "SSO client secret changed")

    binding = connection.execute(
        """
        SELECT id, admin_id, provider_instance_id, protocol, authority, subject,
               display_name, email, created_at, updated_at, last_login_at
        FROM sso_bindings
        """
    ).fetchone()
    require(
        binding
        == (
            "binding-restore-smoke",
            1,
            "provider-restore-smoke",
            "oidc",
            "https://id.example.test",
            "stable-restore-subject",
            "Restore User",
            "restore@example.test",
            CREATED_AT,
            CREATED_AT,
            CREATED_AT,
        ),
        "SSO binding changed",
    )


def assert_migrated(database: Path) -> None:
    connection = sqlite3.connect(database)
    try:
        require(
            connection.execute("PRAGMA integrity_check").fetchone()[0] == "ok",
            "migrated database failed integrity_check",
        )
        require(sqlite_version(connection) == SCHEMA_VERSION, "restore did not migrate to schema 35")
        require(
            "secure_cookies" in sqlite_columns(connection, "sessions"),
            "restore migration did not add sessions.secure_cookies",
        )
        rows = connection.execute(
            """
            SELECT hex(token_hash), login_method, COALESCE(sso_binding_id, ''),
                   credential_epoch, secure_cookies
            FROM sessions ORDER BY created_at, hex(token_hash)
            """
        ).fetchall()
        require(
            rows
            == [
                (PASSWORD_SESSION_HASH.hex().upper(), "password", "", 7, 0),
                (SSO_SESSION_HASH.hex().upper(), "sso", "binding-restore-smoke", 7, 0),
                (WAL_SESSION_HASH.hex().upper(), "sso", "binding-restore-smoke", 7, 0),
            ],
            f"migration did not preserve legacy sessions: {rows}",
        )
        assert_restore_rows(connection)
        flow = connection.execute(
            """
            SELECT id, hex(state_hash), hex(browser_token_hash), purpose,
                   provider_instance_id, config_revision,
                   initiating_session_hash, admin_id, hex(nonce_hash),
                   pkce_verifier, redirect_uri, return_path,
                   created_at, expires_at, claimed_at
            FROM sso_flows
            """
        ).fetchone()
        require(
            flow
            == (
                "flow-restore-smoke",
                STATE_HASH.hex().upper(),
                BROWSER_TOKEN_HASH.hex().upper(),
                "login",
                "provider-restore-smoke",
                7,
                None,
                None,
                NONCE_HASH.hex().upper(),
                "restore-verifier",
                "https://vonet.example.test/api/auth/sso/callback",
                "/settings",
                CREATED_AT,
                EXPIRES_AT,
                0,
            ),
            f"migration did not preserve pending SSO flow: {flow}",
        )
    finally:
        connection.close()


def assert_cleaned(database: Path) -> None:
    connection = sqlite3.connect(database)
    try:
        require(
            connection.execute("PRAGMA integrity_check").fetchone()[0] == "ok",
            "cleaned database failed integrity_check",
        )
        require(
            sqlite_version(connection) == RESTORED_SCHEMA_VERSION,
            "auth-cleanup --no-migrate changed the restored schema version",
        )
        require(
            "secure_cookies" not in sqlite_columns(connection, "sessions"),
            "auth-cleanup --no-migrate applied migration 35",
        )
        counts = {
            table: connection.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]
            for table in ("sessions", "api_tokens", "sso_flows")
        }
        require(
            counts == {"sessions": 0, "api_tokens": 0, "sso_flows": 0},
            f"auth-cleanup did not revoke restored credentials and flows: {counts}",
        )
        epoch = connection.execute(
            "SELECT credential_epoch FROM admins WHERE id = 1"
        ).fetchone()
        require(epoch == (8,), f"auth-cleanup credential epoch is {epoch}, want (8,)")
        assert_sso_restore_rows(connection)
    finally:
        connection.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    full = subparsers.add_parser("assert-full")
    full.add_argument("database", type=Path)

    prepare = subparsers.add_parser("prepare")
    prepare.add_argument("--source", required=True, type=Path)
    prepare.add_argument("--output", required=True, type=Path)

    schema34 = subparsers.add_parser("assert-schema34")
    schema34.add_argument("database", type=Path)

    migrated = subparsers.add_parser("assert-migrated")
    migrated.add_argument("database", type=Path)

    cleaned = subparsers.add_parser("assert-cleaned")
    cleaned.add_argument("database", type=Path)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if args.command == "assert-full":
        assert_full_schema(args.database)
    elif args.command == "prepare":
        prepare_fixture(args.source, args.output)
    elif args.command == "assert-schema34":
        assert_schema34_snapshot(args.database)
    elif args.command == "assert-migrated":
        assert_migrated(args.database)
    elif args.command == "assert-cleaned":
        assert_cleaned(args.database)


if __name__ == "__main__":
    main()
