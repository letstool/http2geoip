# CLAUDE.md — http2geoip

This file provides context for AI-assisted development on the `http2geoip` project.

---

## Project overview

`http2geoip` is a single-binary HTTP gateway that exposes IP geolocation as a JSON REST API.
It is written entirely in Go and embeds all static assets (web UI, favicon, OpenAPI spec) at compile time using `//go:embed` directives, so the resulting binary has zero runtime file dependencies.

The server accepts `POST /api/v1/geoip` requests containing one or more IP addresses and returns structured geographic data sourced from a MaxMind GeoLite2-City database. The database file is downloaded on startup and refreshed daily on a configurable schedule. A peer mode allows syncing the mmdb file from another running `http2geoip` instance instead of querying MaxMind directly.

---

## Repository layout

```
.
├── api/
│   └── swagger.yaml              # OpenAPI 3.1 source (human-editable)
├── build/
│   └── Dockerfile                # Two-stage Docker build (builder + scratch runtime)
├── cmd/
│   └── http2geoip/
│       ├── main.go               # Entire application — single file
│       └── static/
│           ├── favicon.png       # Embedded at build time
│           ├── index.html        # Embedded web UI (dark/light, 15 languages)
│           └── openapi.json      # Embedded OpenAPI spec (generated from swagger.yaml)
├── scripts/
│   ├── 000_init.sh               # go mod tidy
│   ├── 999_test.sh               # Integration smoke tests (curl + jq)
│   ├── linux_build.sh            # Native static binary build
│   ├── linux_run.sh              # Run binary on Linux
│   ├── docker_build.sh           # Build Docker image
│   ├── docker_run.sh             # Run Docker container
│   ├── windows_build.cmd         # Native build on Windows
│   └── windows_run.cmd           # Run binary on Windows
├── go.mod
├── go.sum
├── LICENSE                       # MIT
├── README.md
└── CLAUDE.md                     # This file
```

---

## Key design decisions

- **Single `main.go`**: the entire server logic lives in `cmd/http2geoip/main.go`. There are no internal packages. Keep it that way unless the file grows substantially.
- **Embedded assets**: `favicon.png`, `index.html`, and `openapi.json` are embedded with `//go:embed`. Any change to these files is picked up at the next `go build` — no copy step needed.
- **Static binary**: the build uses `-tags netgo` and `-ldflags "-extldflags -static"` to produce a fully self-contained binary with no libc dependency. Do not introduce `cgo` dependencies.
- **No framework**: the HTTP layer uses only the standard library (`net/http`). Do not add a router or web framework.
- **GeoIP library**: [`github.com/oschwald/geoip2-golang`](https://github.com/oschwald/geoip2-golang) is the primary non-stdlib dependency. It handles mmdb file reading and IP lookup. [`github.com/breml/rootcerts`](https://github.com/breml/rootcerts) is imported as a blank import to embed the Mozilla CA bundle, enabling HTTPS downloads from scratch-based containers.
- **Hot database swap**: the active `*geoip2.Reader` is stored in a `sync/atomic.Value`. Database refreshes replace the value atomically, so in-flight requests are never interrupted and no lock is required on the read path.
- **Two download modes**: when `GEOIP_DB_URL` points to `download.maxmind.com`, the server downloads and extracts the official GeoLite2-City tar.gz archive. Any other URL is treated as a peer `http2geoip` instance and the mmdb is fetched directly from its `/getdb` endpoint. If a peer download fails, the server retries silently every 5 minutes in a background goroutine.
- **Daily scheduler**: a single `time.Timer`-based goroutine triggers `updateDB` every 24 hours at the configured UTC time. The scheduler is a no-op when `GEOIP_DB_URL` is empty.
- **Batch lookups**: a single request may contain either one IP (`ip` field) or a list (`ips` field). The two fields are mutually exclusive. The maximum batch size is enforced server-side via `GEOIP_MAX_IPS` / `-max-ips`.

---

## Environment variables & CLI flags

Every configuration value can be set via an environment variable **or** a command-line flag. The flag always takes priority. Resolution order: **CLI flag → environment variable → hard-coded default**.

| Environment variable  | CLI flag        | Default           | Description                                                                              |
|-----------------------|-----------------|-------------------|------------------------------------------------------------------------------------------|
| `GEOIP_LISTEN_ADDR`   | `-listen`       | `127.0.0.1:8080`  | Listen address and port for the HTTP server.                                             |
| `GEOIP_DB_URL`        | `-db-url`       | *(none)*          | MaxMind download URL (tar.gz) or base URL of a peer `http2geoip` instance.              |
| `GEOIP_DB_DIR`        | `-db-dir`       | `/data`           | Directory used to store and cache the mmdb file and the `.last_update_geoip` marker.          |
| `GEOIP_UPDATE_HOUR`   | `-update-hour`  | `02:00`           | Daily database refresh time in `HH:MM` UTC format.                                      |
| `GEOIP_MAX_IPS`       | `-max-ips`      | `100`             | Maximum number of IP addresses accepted in a single batch request.                       |

CLI flags are parsed with the standard library `flag` package using a sentinel default (`"\x00"` for strings, `-1` for integers) to distinguish between "flag not provided" and "flag explicitly set to empty". Any new configuration entry must expose both a flag and its environment variable counterpart.

---

## Build & run commands

```bash
# Initialise / tidy dependencies
bash scripts/000_init.sh

# Build native static binary -> ./out/http2geoip
bash scripts/linux_build.sh

# Run (edit the script to set GEOIP_DB_URL first)
bash scripts/linux_run.sh

# Build Docker image -> letstool/http2geoip:latest
bash scripts/docker_build.sh

# Run Docker container
bash scripts/docker_run.sh

# Smoke tests (server must be running)
bash scripts/999_test.sh
```

---

## API contract

### Endpoint

```
POST /api/v1/geoip
Content-Type: application/json
```

Exactly one of `ip` or `ips` must be provided per request.

### Request fields

| Field  | Type       | Required          | Notes                                                                                     |
|--------|------------|-------------------|-------------------------------------------------------------------------------------------|
| `ip`   | `string`   | One of `ip`/`ips` | Single IPv4 or IPv6 address to look up.                                                   |
| `ips`  | `string[]` | One of `ip`/`ips` | Batch of IPv4/IPv6 addresses. Maximum count set by `-max-ips` / `GEOIP_MAX_IPS`.         |
| `lang` | `string`   | No                | Language for localized names. One of: `en`, `es`, `fr`, `ja`, `pt-BR`, `ru`, `zh-CN`. Defaults to `en`. |

### Response fields

| Field     | Type         | Description                                                      |
|-----------|--------------|------------------------------------------------------------------|
| `status`  | `string`     | See status values below.                                         |
| `answers` | `Answer[]`   | List of geolocation records. Empty when status is not `SUCCESS`. |

Each `Answer` object:

| Field              | Type              | Description                                         |
|--------------------|-------------------|-----------------------------------------------------|
| `ip`               | `string`          | The queried IP address                              |
| `continent_code`   | `string \| null`  | Two-letter continent code (e.g. `EU`)               |
| `continent_name`   | `string \| null`  | Localized continent name                            |
| `country_isocode`  | `string \| null`  | ISO 3166-1 alpha-2 country code (e.g. `FR`)         |
| `country_name`     | `string \| null`  | Localized country name                              |
| `accuracy`         | `integer \| null` | Accuracy radius in kilometers                       |
| `latitude`         | `number \| null`  | Geographic latitude                                 |
| `longitude`        | `number \| null`  | Geographic longitude                                |
| `time_zone`        | `string \| null`  | IANA time zone identifier (e.g. `Europe/Paris`)     |
| `reg_country_name` | `string \| null`  | Localized name of the registered country            |
| `reg_country_code` | `string \| null`  | ISO 3166-1 alpha-2 code of the registered country   |

### Response status values

| Value      | Meaning                                                            |
|------------|--------------------------------------------------------------------|
| `SUCCESS`  | Lookup succeeded — `answers` is populated                          |
| `NOTFOUND` | No geographic data found for the given IP(s) in the database       |
| `ERROR`    | Request malformed, IP invalid, or database not yet initialized     |

### Other endpoints

| Method | Path            | Description                                                               |
|--------|-----------------|---------------------------------------------------------------------------|
| `GET`  | `/`             | Embedded interactive web UI                                               |
| `GET`  | `/openapi.json` | OpenAPI 3.1 specification                                                 |
| `GET`  | `/favicon.png`  | Application icon                                                          |
| `GET`  | `/getdb`        | Serves the current mmdb file for peer-mode clients                        |

---

## Web UI

The UI is a self-contained single-file HTML/JS/CSS application embedded in the binary.

- **Themes**: dark and light, switchable via a toggle button.
- **Languages**: 15 locales built in — Arabic (`ar`), Bengali (`bn`), Chinese (`zh`), German (`de`), English (`en`), Spanish (`es`), French (`fr`), Hindi (`hi`), Indonesian (`id`), Japanese (`ja`), Korean (`ko`), Portuguese (`pt`), Russian (`ru`), Urdu (`ur`), Vietnamese (`vi`). Language is selected from a dropdown; Arabic and Urdu automatically switch the layout to right-to-left.
- The UI calls `POST /api/v1/geoip` and renders results in a table with continent, country, registered country, coordinates, accuracy radius, and time zone.
- The OpenAPI spec is also served at `/openapi.json` for use with tools such as Swagger UI or Postman.

To modify the UI, edit `cmd/http2geoip/static/index.html` and rebuild.  
To update the API spec, edit `api/swagger.yaml`, regenerate `openapi.json`, and rebuild.

---

## Adding a new configuration parameter

1. Declare the global variable in the `var` block in `main.go`.
2. Add an environment variable name (uppercase, `GEOIP_` prefix) and document it in this file and in `README.md`.
3. Add a corresponding CLI flag in `main()` using the `flag` package, with a sentinel default to distinguish "not provided" from an explicit value.
4. Apply the resolution logic: flag wins if non-sentinel, then env var, then hard-coded default.

---

## Constraints & conventions

- Go version: **1.24+**
- No `cgo`. Keep `CGO_ENABLED=0`.
- No additional HTTP frameworks or routers.
- All logic stays in `cmd/http2geoip/main.go` unless a strong reason arises to split it.
- Error responses always return a `geoResponse` JSON body — never a plain-text error.
- The server never logs request bodies; avoid adding logging that could expose queried IP addresses.
- All code, identifiers, comments, and documentation must be written in **English**. No icons, emoji, or non-ASCII decorations in comments or doc strings.
- **Every configuration environment variable must have a corresponding command-line flag** (parsed via `flag` from the standard library). The flag always takes priority over the environment variable. The resolution order is: CLI flag → environment variable → hard-coded default.

---

## AI-assisted development

This project was developed with the assistance of **Claude Sonnet 4.6** by Anthropic.
