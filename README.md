<p align="center">
  <img src="web/public/logo.svg" width="240" alt="VoNet">
</p>

<h1 align="center">VoNet</h1>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27.3-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="React" src="https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react&logoColor=111111">
  <img alt="TypeScript" src="https://img.shields.io/badge/TypeScript-5.8-3178C6?style=flat-square&logo=typescript&logoColor=white">
  <img alt="Vite" src="https://img.shields.io/badge/Vite-7-646CFF?style=flat-square&logo=vite&logoColor=white">
  <img alt="Tailwind CSS" src="https://img.shields.io/badge/Tailwind_CSS-3-06B6D4?style=flat-square&logo=tailwindcss&logoColor=white">
  <img alt="SQLite" src="https://img.shields.io/badge/SQLite-Embedded-003B57?style=flat-square&logo=sqlite&logoColor=white">
</p>

<p align="center">
  <img alt="Linux" src="https://img.shields.io/badge/Linux-amd64_%7C_386_%7C_arm64_%7C_aarch64_%7C_armv7-FCC624?style=flat-square&logo=linux&logoColor=111111">
  <img alt="Docker" src="https://img.shields.io/badge/Docker-Multi--Arch-2496ED?style=flat-square&logo=docker&logoColor=white">
  <img alt="WiFi Calling" src="https://img.shields.io/badge/WiFi_Calling-IMS_SMS-7B1FA2?style=flat-square">
  <img alt="eSIM" src="https://img.shields.io/badge/eSIM-LPA_%2F_eUICC-009688?style=flat-square">
  <img alt="Telegram" src="https://img.shields.io/badge/Telegram-Bot-26A5E4?style=flat-square&logo=telegram&logoColor=white">
  <img alt="GitHub Actions" src="https://img.shields.io/badge/GitHub_Actions-Release-2088FF?style=flat-square&logo=githubactions&logoColor=white">
</p>

**English** | [العربية](docs/README.ar.md) | [简体中文](docs/README.zh-CN.md) | [繁體中文](docs/README.zh-TW.md) | [Français](docs/README.fr.md) | [Русский](docs/README.ru.md) | [Español](docs/README.es.md) | [日本語](docs/README.ja.md)

VoNet is an open-source web control panel and engineering toolkit for Quectel EC20/EC25-class and China Mobile ML307 series cellular modems. It combines modem discovery, live radio status, AT and USSD terminals, SMS, WiFi Calling, eSIM management, network selection, proxy routing, notifications, audit logs, and release automation in one self-contained service.

The backend is written in Go, the interface is built with React and TypeScript, and the production frontend is embedded into the Go binary. A single executable contains the web application and uses SQLite for persistent state.

<p align="center">
  <img src="img\image.png">
  <img src="img\image-1.png">
</p>

## Features

| Area | What VoNet provides |
| --- | --- |
| Device management | Automatic serial/USB discovery, multiple modem support, friendly device names, live overview updates, module restart, flight mode, and USB networking mode controls. |
| Radio and network | Registration status, operator, signal metrics, RSRP/RSRQ/SINR, network mode, band, channel, operator scanning, automatic or manual network selection, and LTE cell querying and single-cell lock/unlock (EARFCN/PCI). |
| AT and USSD | Interactive AT terminal, command history, raw modem responses, USSD start/continue/cancel flows, and clear modem error reporting. |
| SMS | Direct cellular and IMS SMS transmission, inbound synchronization, multipart handling, delivery reports, conversation history, unread state, timestamps, and per-message delivery status. |
| WiFi Calling | IKEv2/ePDG tunnel setup, EAP-AKA authentication, IMS registration, IMS SMS, reconnect controls, status diagnostics, and per-device routing. |
| eSIM and eUICC | eUICC discovery, EID and production information, certificate metadata, multi-eUICC inventory, installed profile listing, enable/disable/switch operations, download, rename, and delete operations when supported by the card. |
| Card policy | ICCID-based WiFi Calling and flight-mode behavior with immediate policy application. |
| Proxy routing | Upstream SOCKS routing, device bindings, country rules, TCP reachability checks, and UDP Associate checks for WiFi Calling data paths. |
| Notifications | New inbound SMS forwarding through Telegram, Bark, email, Pushplus, and signed webhooks. Each SMS is delivered as an individual notification. |
| Telegram bot | Device status, installed-profile listing and switching, WiFi Calling controls, and SMS sending. Sensitive actions require administrator confirmation. |
| Operations | Password and SSO authentication (Pocket ID, GitHub, Generic OAuth2), CSRF protection, access policies, audit events, live logs, log retention, health checks, responsive layout, dark mode, and English/Chinese application UI. |
| Distribution | Static Linux binaries, systemd installation script, self-update with SHA-256 verification, Docker image, GHCR publishing, and GitHub Actions release builds. |

## Supported hardware

VoNet targets Qualcomm-based Quectel and China Mobile ML307 series modules that expose compatible AT, QMI, serial, and USB networking interfaces, including:

- Quectel EC20
- Quectel EC25
- Quectel EG25 family
- Compatible EG600 and related modules
- China Mobile ML307 series (e.g. ML307A-E5, various OEMs)

Available features depend on the module firmware, USB composition, SIM/eSIM capabilities, host drivers, radio network, and carrier configuration.

## Installation

### One-click Linux installation

As root (including OpenWrt/Kwrt, where `sudo` is normally absent):

```bash
curl -fsSL https://raw.githubusercontent.com/springai/VoNet/master/scripts/install.sh | bash
```

From a normal user on a distribution with sudo:

```bash
curl -fsSL https://raw.githubusercontent.com/springai/VoNet/master/scripts/install.sh | sudo bash
```

Check the host's VoWiFi/XFRM prerequisites without installing VoNet:

```bash
curl -fsSL https://raw.githubusercontent.com/springai/VoNet/master/scripts/install.sh | bash -s -- --check-env
```

Install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/springai/VoNet/master/scripts/install.sh -o install.sh
sudo bash install.sh 0.0.2
```

VoWiFi IMS requires Linux XFRM/IPsec. On OpenWrt/Kwrt the installer attempts
to install matching `ip-full`, `kmod-ipsec`, `kmod-ipsec4/6`,
`kmod-crypto-authenc`, AES-CBC and SHA1 packages from the firmware's own feed.
If matching kernel modules are unavailable, use a firmware that includes them;
never force-install kmods built for a different kernel.

If your kernel cannot provide XFRM/IPsec and you only need non-VoWiFi features
such as cellular SMS or data, install with `--skip-vowifi-check`:

```bash
curl -fsSL https://raw.githubusercontent.com/springai/VoNet/master/scripts/install.sh -o install.sh
sudo bash install.sh --skip-vowifi-check
```

The installer:

- detects `amd64`, `386`, `arm64`, `aarch64`, or `armv7`;
- downloads the matching GitHub Release binary;
- verifies it against `SHA256SUMS`;
- installs VoNet under `/opt/vonet`;
- creates a hardened systemd service with the hardware and network access required by VoNet;
- stores runtime configuration in `/etc/vonet/env`;
- generates a random initial administrator password on first installation.

After installation, open:

```text
http://<server-address>:7575
```

### Manual binary installation

Download the matching binary and `SHA256SUMS` from GitHub Releases:

| Platform | Release file |
| --- | --- |
| Linux x86-64 | `vonet-linux-amd64` |
| Linux x86 32-bit | `vonet-linux-386` |
| Linux ARM64 | `vonet-linux-arm64` |
| Linux AArch64 | `vonet-linux-aarch64` |
| Linux ARMv7 | `vonet-linux-armv7` |

Verify and install it:

```bash
sha256sum -c SHA256SUMS --ignore-missing
sudo install -d -m 0755 /opt/vonet/bin /opt/vonet/data
sudo install -m 0755 vonet-linux-amd64 /opt/vonet/bin/vonet
read -rsp "Admin password: " VONET_BOOTSTRAP_PASSWORD; echo
printf '%s\n' "$VONET_BOOTSTRAP_PASSWORD" | sudo /opt/vonet/bin/vonet bootstrap-admin
unset VONET_BOOTSTRAP_PASSWORD
sudo env \
  VONET_DATABASE_PATH=/opt/vonet/data/vonet.db \
  /opt/vonet/bin/vonet serve
```

```bash
mkdir -p /opt/vonet/bin /opt/vonet/data
chmod 0755 /opt/vonet/bin /opt/vonet/data

cp vonet-linux-amd64 /opt/vonet/bin/vonet
chmod 0755 /opt/vonet/bin/vonet

read -rsp "Admin password: " VONET_BOOTSTRAP_PASSWORD; echo
printf '%s\n' "$VONET_BOOTSTRAP_PASSWORD" | /opt/vonet/bin/vonet bootstrap-admin
env \
  VONET_DATABASE_PATH=/opt/vonet/data/vonet.db \
  /opt/vonet/bin/vonet serve
```

This manual command runs VoNet in the foreground. Use `vonet serve` so the
process starts the server directly; running `vonet` without arguments as root
on a TTY opens the interactive management menu instead. Use the one-click
installer when a managed systemd service and automatic restart are required.

### Docker

For a Linux host that must discover every attached supported Quectel modem and
continue seeing USB hot-plug events, run VoNet in hardware-access mode:

```bash
docker pull ghcr.io/mengmengcode/vonet:latest

read -rsp "Admin password: " VONET_BOOTSTRAP_PASSWORD; echo
printf '%s\n' "$VONET_BOOTSTRAP_PASSWORD" | docker run --rm -i \
  --user 0:0 \
  -v vonet-data:/opt/vonet/data \
  --entrypoint /opt/vonet/bin/vonet \
  ghcr.io/mengmengcode/vonet:latest bootstrap-admin
unset VONET_BOOTSTRAP_PASSWORD

docker run -d \
  --name vonet \
  --restart unless-stopped \
  --network host \
  --privileged \
  --user 0:0 \
  -v vonet-data:/opt/vonet/data \
  -v /dev:/dev \
  -v /sys:/sys:ro \
  ghcr.io/mengmengcode/vonet:latest
```

Open `http://<server-address>:7575` after the container starts. Host networking
is required so QMI network interfaces remain visible to VoNet, while privileged
device access is required for serial ports, QMI control nodes, TUN interfaces,
network configuration, and devices added after the container starts. The
`/dev` bind mount makes new `ttyUSB*`, `ttyACM*`, `cdc-wdm*`, and MHI
`wwan*` nodes visible without recreating the container.

This mode intentionally gives VoNet broad access to the host's devices and
network stack. Use it only on a trusted Linux host. The automatic discovery
identifies supported Quectel USB modems (USB vendor ID `2c7c`) and China Mobile ML307 series modules by product string, and PCIe/MHI
modems exposed through the Linux WWAN subsystem; it does not identify arbitrary
modem layouts. Mapping only individual nodes with `--device`, such as
`/dev/ttyUSB2`, `/dev/cdc-wdm0`, or `/dev/wwan0qmi0`, limits the container to
those fixed nodes and does not provide complete multi-device or hot-plug discovery.

On OpenWrt hosts, the fw4 input chain drops new connections from interfaces it
does not manage, including VoNet's per-session VoWiFi TUN. VoNet installs narrow
`vonet-ims-*` accept rules for P-CSCF traffic using the bundled `nft`. Set
`VONET_NFT` to a wrapper for the host's `nft` when the container cannot read the
host ruleset. The wrapper must use the host's actual musl loader name; ARMv7
images commonly use `ld-musl-armhf.so.1`.

For example, create a host-side wrapper and mount it into the container:

```bash
cat > /opt/vonet-host-nft <<'EOF'
#!/bin/sh
exec /host/lib/ld-musl-$(uname -m).so.1 \
  --library-path /host/usr/lib:/host/lib \
  /host/usr/sbin/nft "$@"
EOF
chmod 755 /opt/vonet-host-nft

# Add these options to the docker run command above:
#   -v /usr:/host/usr:ro \
#   -v /lib:/host/lib:ro \
#   -v /opt/vonet-host-nft:/usr/local/bin/host-nft:ro \
#   -e VONET_NFT=/usr/local/bin/host-nft
```

On ARMv7, replace the loader path with the actual host loader, for example
`/host/lib/ld-musl-armhf.so.1`.

The GHCR image is published for `linux/amd64` and `linux/arm64`.

> [!TIP]
> **NAS / QNAP Container Station Deployment Note**:
> On NAS operating systems like QNAP QTS / QuTS hero (Container Station), custom non-root administrator accounts and volume isolation mechanisms may cause Docker named volumes (e.g. `-v vonet-data:/opt/vonet/data`) to resolve to different isolated paths between the one-off `bootstrap-admin` initialization and the daemon service container, leading to "Incorrect password" errors during Web login.
> For NAS environments, it is strongly recommended to replace named volumes with a host absolute path bind mount (e.g. `-v /share/Container/vonet/data:/opt/vonet/data` on QNAP) for both initialization and runtime to guarantee consistent SQLite database persistence.

### USB SIM readers

USB SIM readers use the Linux PC/SC service. The one-click installer installs
and starts `pcscd` plus the CCID driver automatically on supported package
managers. On Debian/Ubuntu, the equivalent manual setup is
`apt install pcscd libccid`. If USB sees a CCID reader but PC/SC is unavailable,
VoNet keeps the reader visible in the add-device dialog and reports the missing
service or driver instead of silently hiding it.

### QMI command-line utilities

VoNet uses `qmicli` to verify that a QMI control channel is ready and
`qmi-proxy` to multiplex access to it. Packet-data sessions are managed by the
built-in QMI WDS client instead of `qmi-network` CID/PDH state files. The
one-click installer installs and verifies the corresponding utilities. For manual deployment,
Debian/Ubuntu uses `apt install libqmi-utils`; Arch Linux uses
`pacman -S libqmi`, Alpine uses `apk add qmi-utils`, and OpenWrt uses
`opkg install qmi-utils`.

`vonet doctor --repair-dji-qmi` checks for `qmicli` before changing any USB
driver binding or asserting DTR. If the utility is unavailable, the command
stops with an installation hint and leaves the current device state untouched.

### AT cellular host data

AT-backed ECM/RNDIS data requires Linux, `iproute2`, and BusyBox `udhcpc`.
VoNet must exclusively manage the modem network interface; disable competing
DHCP clients and NetworkManager profiles for that interface. VoNet maintains
an IPv4 DHCP lease and marked policy routes without changing the system
default route or resolver. This path currently requires IPv4-capable service.

## Configuration

VoNet reads an optional JSON configuration file from `VONET_CONFIG`, then applies `VONET_*` environment variables. Environment variables take precedence.

| Environment variable | Default | Description |
| --- | --- | --- |
| `VONET_ADDR` | `0.0.0.0:7575` | HTTP listen address. |
| `VONET_DATABASE_PATH` | `./data/vonet.db` | SQLite database path. |
| `VONET_SESSION_TTL` | `24h` | Authentication session lifetime. |
| `VONET_SECURE_COOKIES` | `false` | Marks session cookies as secure when HTTPS is used. |
| `VONET_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout. |
| `VONET_MAX_REQUEST_BODY_BYTES` | `1048576` | Maximum API request body size. |
| `VONET_MCP_ENABLED` | `false` | Serve the MCP endpoint at `/mcp` for AI agents. Set `true` to enable it. |
| `VONET_PROBE_UNKNOWN_USB` | `false` | Opt in to probing unrecognized USB serial devices with AT commands. Probing changes serial settings and clears input; enable only when those ports are dedicated to modems. Verified modem identities are discovered automatically. JSON key: `probe_unknown_usb`. |
| `VONET_REPO` | `springai/VoNet` | Trusted GitHub repository used by the self-updater, in `owner/name` form. |
| `GITHUB_TOKEN` | empty | Optional GitHub token for private repositories or higher API limits. |

User-supplied Apple carrier bundles can be converted into reviewable,
allow-listed carrier profiles with `vonet carrier import-ipcc`; see
[docs/CARRIER_IPCC_IMPORT.md](docs/CARRIER_IPCC_IMPORT.md).

Administrator credentials are stored only in SQLite. Initialize an empty
database once with `vonet bootstrap-admin`; environment variables and JSON
configuration cannot set or overwrite the administrator username or password.

Do not store Telegram tokens, SMTP passwords, webhook secrets, SIM credentials, or other private data in the repository. Configure them through the application settings or protected environment files.

## API tokens and MCP

Non-browser clients authenticate against the same `/api/...` endpoints with a
bearer token instead of the browser's session cookie and CSRF flow:

```bash
vonet token create --read-only --ttl 720h ci-runner   # prints the token once
curl -H "Authorization: Bearer vonet_..." http://127.0.0.1:7575/api/devices
```

`vonet token list` and `vonet token revoke <name>` manage the inventory.
Tokens act with administrator privileges; a `--read-only` token may only use
`GET`, `HEAD`, and `OPTIONS` operations and cannot send SMS, switch eSIM
profiles, trigger reconnects, or use the other documented hardware side
effects. One documented database side effect remains: `GET /api/sms/thread`
marks its messages as read. Rotating the administrator password or applying a
binary update revokes every session and token. Create tokens after the first
`bootstrap-admin`. Remote bearer clients also need a network access policy
that admits their source address; the default `internal` mode only serves
private networks.

When `VONET_MCP_ENABLED=true`, the service exposes an MCP (Model Context
Protocol) endpoint at `http://<host>:7575/mcp` using the Streamable HTTP
transport and bearer-token authentication. Tools cover device status, SMS
threads and guarded sending, eSIM profile listing and coordinated switching,
and WiFi Calling status/reconnect. Sensitive operations such as raw AT, eSIM
download/delete, SIM PIN, network selection and system update are not
exposed, and tool output does not include IMSI, SIM PIN or activation codes.
A read-only token receives a tool list without the write tools. SMS sending
uses the same VoNet destination policy and global hourly limit as the web UI,
with no destination-country block. MCP messages use the cellular AT path and
are not written to the web conversation history, so they do not reproduce the
web UI's IMS-first/O2 SMS behavior.

Client configuration (Claude Code as the example):

```json
{
  "mcpServers": {
    "vonet": {
      "type": "http",
      "url": "http://192.168.1.10:7575/mcp",
      "headers": { "Authorization": "Bearer vonet_..." }
    }
  }
}
```

See [docs/API_MCP_DEVELOPMENT_PLAN.md](docs/API_MCP_DEVELOPMENT_PLAN.md) for
the design record.

## Single sign-on

The login page can use Pocket ID, GitHub, or a generic OAuth2/OpenID Connect
provider while password login remains available. SSO stays disabled until a
provider is configured and its external identity is bound to the single
administrator.

`PublicOrigin` must contain only the public scheme, host, and optional port.
The callback is fixed at `/api/auth/sso/callback`. Production deployments must
use HTTPS and set `VONET_SECURE_COOKIES=true`; HTTP is accepted only for
loopback development. SSO settings and account binding are browser-session
operations protected by session-bound CSRF and cannot be managed with API
tokens or MCP.

The SQLite database stores the provider configuration, binding metadata, and
a client secret in a setting marked sensitive. Treat database backups and WAL
files as credential material. Applying a binary update or rotating the
administrator password revokes sessions, API tokens, and pending SSO flows.
After restoring a database backup, revoke old credentials and clear pending
flows before exposing the service again.

### Configure identity providers

In **Settings → Single sign-on (SSO)**, select one provider and follow the
same lifecycle: save the provider while SSO is disabled, check its
configuration, bind one external account, and only then enable SSO. Other
external accounts cannot register or become administrators, including
accounts that share the bound account's email address.

For Pocket ID:

1. Use an HTTPS root-path origin for VoNet, such as `https://vonet.example.com`,
   and set `VONET_SECURE_COOKIES=true`. Select **Pocket ID**. The public origin
   initially uses the browser address; edit **VoNet public origin** when needed.
   The callback and Copy button are available before credentials are saved:
   `https://vonet.example.com/api/auth/sso/callback`.
2. In Pocket ID, create a separate **Confidential** OIDC client for VoNet.
   Register the exact callback and confirm that the intended user or group may
   use the client. Give each application its own client and local session.
3. Enter Pocket ID's exact public `APP_URL` as the Issuer and enter the Client
   ID and Client Secret issued for that client. VoNet handles scopes and PKCE
   S256, and selects `client_secret_basic` before `client_secret_post` from
   validated Discovery metadata. Save while SSO remains disabled.
4. Check the saved configuration, then bind the account through Pocket ID's
   Passkey login and consent. The configuration check validates metadata and
   connectivity; only the real login verifies the secret and user access.
5. Enable and save SSO, then use a private browser window to verify login,
   return to the requested page, and complete the first state-changing request.

Issuer matching includes its exact path and trailing slash. Pocket ID binds
the verified ID Token's `issuer + sub`; UserInfo and email claims are not
required. Discovery, authorization, token and JWKS endpoints must use the
configured Issuer origin. A deployment that publishes a different backend
origin through `INTERNAL_APP_URL` is not supported; keep the public `APP_URL`
as the Issuer.

For GitHub, create a GitHub.com OAuth App with the same exact callback, select
**GitHub**, enter its Client ID and Secret, and follow the save, check, bind,
enable sequence. VoNet uses the fixed GitHub endpoints, defaults to
`read:user`, requires PKCE S256, and binds the stable numeric account ID.
GitHub Enterprise is not supported by this preset.

For Generic OAuth2, supply authorization, token and UserInfo URLs in addition
to client credentials. Configure scopes, the top-level user ID field
(default `id`), token authentication and PKCE compatibility. UserInfo is
requested with a Bearer access token; the ID must be a nonempty string or an
integer, and large integer IDs retain every digit. The defaults are
`client_secret_post` and PKCE S256. Disable PKCE only for a provider without
PKCE support. Generic OAuth2 does not perform OIDC ID-token verification.

Changing provider, Client ID, Issuer, or a Generic identity endpoint/user ID
field creates a new identity namespace. The saved secret is discarded and the
external binding must be explicitly reset before SSO can be enabled again.

### Network, deployment and TLS

The existing source-network policy also covers all SSO routes. `PublicOrigin`
is the root-path public origin and is never derived from forwarded Host
headers. Production requires HTTPS and `VONET_SECURE_COOKIES=true`; HTTP is
accepted only for loopback development.

The VoNet process must resolve and connect to Discovery, token, JWKS and
UserInfo endpoints. Every DNS candidate and actual dial address is checked:
special-use, loopback, link-local, cloud metadata and other privileged
destinations are rejected. Pocket ID and generic HTTPS endpoints may target
trusted public or private networks. Generic HTTP token/UserInfo endpoints are
limited to explicitly configured private backends. GitHub remains public-only.

Private CA certificates must be installed in the host or container trust
store. There is no TLS verification bypass. A reverse proxy must preserve the
configured public origin. Root-path deployment is required; VoNet does not
support publishing SSO beneath a URL prefix and does not derive callback URLs
from forwarded Host headers.

The supplied Compose deployment uses host networking. Verify DNS, routing and
CA trust from the actual container instead of assuming bridge-network service
names are available. For local Vite development, use one consistent loopback
origin, such as `http://127.0.0.1:5173`, for both the public origin and the
registered callback. IdP transport exceptions belong only in test fixtures.

### Sessions, CSRF and credentials

Session and CSRF cookies remain `SameSite=Strict`; short-lived SSO flow
cookies use `SameSite=Lax` for top-level GET callbacks. SSO settings and
binding operations require a browser session and session-bound CSRF. Bearer
tokens and MCP cannot manage SSO.

The client secret is stored as a sensitive SQLite setting and is never
returned to the browser. Third-party access and identity tokens are not
persisted. Saving or disabling SSO and unbinding an account revoke affected
SSO sessions; password sessions and API tokens remain valid by policy.
Changing the administrator password revokes sessions and API tokens, advances
the credential epoch, cancels pending flows and preserves the external
binding.

Signing out ends only the current VoNet session. Disabling an account at the
IdP or signing out of Pocket ID does not immediately revoke an existing VoNet
session; it expires according to `VONET_SESSION_TTL`, or can be revoked sooner
by disabling SSO or unbinding the account in VoNet. The public capabilities
endpoint is `GET /api/auth/providers`.

Configuration revision conflicts return `409 sso_config_changed`; changing an
identity source returns `409 sso_rebind_required` until the old binding is
explicitly reset. Existing bindings can also return `409 sso_binding_exists`
or `409 sso_identity_not_bound` when the requested transition is incomplete.

### Upgrade, backup and rollback

SSO uses VoNet schema 34 while preserving migration 33 API tokens and all
local business tables. SSO remains disabled until configured and bound.
Before upgrading, stop the service and back up the complete data directory,
including SQLite, WAL/SHM, TLS, plugins and `/etc/vonet/env`. A live copy must
use SQLite's backup API or `.backup`; copying only the main database file is
not reliable.

After restoring any database backup, run:

```bash
vonet auth-cleanup --database /opt/vonet/data/vonet.db
```

This preserves SSO configuration and the external binding, but revokes
restored sessions and API tokens, advances the credential epoch and removes
pending SSO flows before the service is exposed. Schema 34 has no automatic
downgrade; rollback requires restoring the matching pre-upgrade snapshot.

For Docker, update by pulling the image and recreating the container. Before a
production release, verify the HTTPS root-path callback and secure cookies,
complete a real Pocket ID Passkey/access-policy test, confirm DNS, routing and
CA trust from the deployed Compose container, and rehearse the schema 34
upgrade, `vonet auth-cleanup`, and rollback procedure.

### Cleanup health

VoNet runs SSO flow cleanup at startup and once per minute. When a cleanup run
fails, `/readyz` returns HTTP `503` with
`{"status":"degraded","reason":"sso_cleanup"}`, and `/metrics` reports
`vonet_sso_cleanup_healthy 0`. After a subsequent cleanup succeeds, `/readyz`
returns HTTP `200` with `"status":"ready"` and `"sso_cleanup_healthy":true`,
while `/metrics` reports `vonet_sso_cleanup_healthy 1`.

`vonet auth-cleanup` is a standalone operational command for restoring a
database backup or deliberately retiring restored or stale authentication
state. It requires users to authenticate again and create new API tokens while
preserving SSO provider configuration and the external binding, and it emits
only an aggregate completion result. Keep passwords, session/API token values,
SSO secrets, and backup/database contents out of logs, support reports and
health output; `/readyz` and `/metrics` expose aggregate cleanup state only.

## Telegram bot

When Telegram notifications are enabled and both Chat ID and Admin ID are configured, the bot supports:

```text
/status [device]
/esim <device>
/switch <device> <iccid>
/wfc <device> <status|on|off|reconnect>
/sms <device> <number> <message>
/ussd <device> <code>
/ussd_reply <session> <input>
/ussd_cancel <session>
```

Profile switching and SMS submission use one-time confirmation buttons. The bot does not expose eSIM download, delete, or rename commands.

The web terminal and Telegram share USSD routing. With WiFi Calling enabled, requests use IMS/USSI and return a readiness error until IMS registers, without falling back to cellular AT+CUSD. With WiFi Calling disabled, requests use cellular USSD. Menu replies and cancellation stay bound to the original device and dialog. Cancellation errors are reported and retain the session for retry; an uncertain submission is never automatically resubmitted over another channel.

USSI dialog handling follows [3GPP TS 24.390](https://www.etsi.org/deliver/etsi_ts/124300_124399/124390/18.00.00_60/ts_124390v180000p.pdf). A failed BYE retains a cancellation record, but further menu input is blocked once cancellation has started ([RFC 3261, section 15.1.1](https://www.rfc-editor.org/rfc/rfc3261.html#section-15.1.1)).

## Updating

Check for a newer GitHub Release:

```bash
vonet update --check --repo springai/VoNet
```

Install the latest release:

```bash
sudo vonet update --repo springai/VoNet
```

The updater downloads the binary matching the current Linux architecture, verifies it with the published `SHA256SUMS`, replaces the executable atomically, and restarts the `vonet` systemd service when available.

For Docker installations:

```bash
docker pull ghcr.io/mengmengcode/vonet:latest
```

Recreate the container after pulling the new image.

## Development

Requirements:

- Go 1.27.3 or newer
- Node.js 20 or newer
- npm

Run the frontend development server:

```bash
cd web
npm install
npm run dev
```

Build the embedded frontend and start the backend:

```bash
cd web
npm run build
cd ..
go run ./cmd/vonet
```

Run all tests:

```bash
go test ./...
```

Build a production binary:

```bash
go build -trimpath -ldflags "-s -w" -o vonet ./cmd/vonet
```

```bash
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -trimpath -ldflags "-s -w" -o vonet ./cmd/vonet
```

```bash
$env:GOOS="linux"; $env:GOARCH="arm64"; go build -trimpath -ldflags "-s -w" -o vonet ./cmd/vonet
```

```bash
nohup sudo env \
  VONET_DATABASE_PATH=/opt/vonet/data/vonet.db \
  /opt/vonet/bin/vonet serve > /var/log/vonet.log 2>&1 &
```
```bash
nohup env \
  VONET_DATABASE_PATH=/opt/vonet/data/vonet.db \
  /opt/vonet/bin/vonet serve > /var/log/vonet.log 2>&1 &
```

```bash
sudo env \
  VONET_DATABASE_PATH=/opt/vonet/data/vonet.db \
  /opt/vonet/bin/vonet serve
```


## Release automation

Pushing a version tag starts two GitHub Actions workflows:

- `release-binaries` builds and publishes `amd64`, `386`, `arm64`, `aarch64`, and `armv7` binaries plus `SHA256SUMS`.
- `docker` builds and publishes a multi-architecture image to GitHub Container Registry.

```bash
git tag v0.2.0
git push origin v0.2.0
```

## Project layout

```text
cmd/vonet/                  Application entry point and CLI
internal/device/            Modem discovery and device control
internal/modem/             AT session and response handling
internal/server/            HTTP API, notifications, and embedded web server
internal/store/             SQLite persistence
internal/update/            GitHub Release self-updater
internal/vowifi/            IKE, EAP-AKA, IMS, and WiFi Calling runtime
scripts/install.sh          Linux installer and updater
web/src/                    React and TypeScript frontend
.github/workflows/          Binary and Docker release automation
```

## Responsible use

Cellular modem and eSIM operations can affect subscriber service, stored profiles, network registration, and hardware state. Keep backups, review destructive actions carefully, and use the software only in lawful environments where you are permitted to operate the connected hardware and network resources.

VoNet does not bypass carrier authentication, network policy, hardware security, or eSIM trust requirements. Support for an operation means that VoNet can request it from the modem or eUICC; the device, profile, network, or carrier may still reject it.

## Contributing

Issues and pull requests are welcome. Keep changes focused, include tests where practical, avoid committing credentials or subscriber data, and document hardware-specific behavior clearly.

Before submitting a change:

```bash
go test ./...
cd web && npm run build
```

## Thanks
- [Nodeseek.com](https://www.nodeseek.com) — A community dedicated to servers
- [Linux.do](https://linux.do) — An inspiring tech community
- [VoCat](https://github.com/MengMengCode/VoCat) — A cellular modem testing and control toolkit
- [iniwex5](https://github.com/iniwex5) — Style and Functionality Guidelines

## License

See [LICENSE](LICENSE).
