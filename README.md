# TRixie

**QR-Based Item Finder System**

TRixie helps you get your lost belongings back. Register your items, generate QR code stickers, and when someone finds your stuff they can scan the code and instantly reach you — no personal details exposed until they scan.

---

## How It Works

1. **Register** your items and contact info in the admin panel.
2. **Generate** QR code stickers for each item.
3. **Stick** them on your keys, laptop, luggage, wallet — anything you might lose.
4. **Relax.** When a finder scans the QR code, they see your contact options (email, call, SMS, WhatsApp) and can reach out immediately.

Every QR link is cryptographically signed, so nobody can guess or tamper with item URLs - which prevents web-scraping.

---

## Architecture

TRixie is split into two lightweight Go services sharing a single SQLite database:

| Service | Port | Purpose |
|---------|------|---------|
| **Admin App** | `9090` | Manage owners, items, prefixes, QR codes, and settings |
| **Public App** | `8080` | Finder-facing pages — scan a QR code, see contact info |

The public app opens the database in **read-only** mode, so there's no risk of accidental writes from the public side.

---

## Features

### Admin Panel

- **Owner Management** — Add owners with name, email, phone, and granular contact display options (call, SMS, WhatsApp).
- **Item Management** — Create items with unique codes, descriptions, and assign one or more owners. Items support many-to-many ownership.
- **Identifier Prefixes** — Define reusable prefixes (e.g. `KEY`, `LAPTOP`, `BAG`) with auto-incrementing counters for consistent item codes like `KEY-1`, `KEY-2`, etc.
- **QR Code Generation** — Generate individual or batch QR codes with configurable size, error correction level, border width, and optional text overlays (owner initials or last name).
- **Batch Export** — Print QR codes directly or download them as a ZIP archive. Track which items have been exported.
- **Unexported Items** — Quickly find and export QR codes for newly added items that haven't been printed yet.
- **Search & Sort** — Search items by code or description; sort by any column. Single-result searches jump straight to the edit page.
- **Setup Wizard** — Guided first-run setup to configure your base URL and create your first owners and prefixes.
- **Settings** — Configure base URL, item path prefix, QR defaults, and view your signature key.
- **REST API** — JSON endpoints for owners, items, and prefixes at `/api/owners`, `/api/items`, and `/api/prefixes`.
- **Health Check** — `/health` endpoint for monitoring.

### Public App

- **Item Lookup** — Finders scan a QR code and land on a clean contact page for that item's owner(s).
- **Signature Verification** — Every item URL includes an HMAC signature. Invalid or missing signatures are rejected with a helpful error page.
- **Internationalization** — Automatic language detection via `Accept-Language` header. Ships with English and German; easily extensible.
- **Pre-built Contact Links** — One-tap buttons for email, phone call, SMS, and WhatsApp with pre-filled messages like *"Hey! I found your item (KEY-42)"*.
- **Health Check** — `/health` endpoint for monitoring.

### Security

- **HMAC-SHA256 Signed URLs** — Item URLs contain a truncated HMAC signature, preventing enumeration or forgery.
- **Read-Only Public Database** — The public app cannot modify data.
- **No Personal Data in QR Codes** — QR codes only encode a signed URL, not owner details.

---

## Quick Start

### Prerequisites

- **Go 1.21+**
- **GCC** and **SQLite3** development libraries (for `go-sqlite3`)
- **Make** (optional, but convenient)

### Build & Run

```
# Clone the repo
git clone https://github.com/mrwiora/TRixie.git
cd TRixie

# Build both apps
make build

# Terminal 1 — start the admin app
make run-admin

# Terminal 2 — start the public app
make run-public
```

Open [http://localhost:9090](http://localhost:9090) for the admin panel. The setup wizard will walk you through initial configuration.

Open [http://localhost:8080](http://localhost:8080) for the public-facing app.

### Load Sample Data

```
make sample
```

This seeds the database with example owners and items so you can explore right away.

### Other Useful Commands

| Command | Description |
|---------|-------------|
| `make build` | Build both binaries |
| `make build-admin` | Build admin app only |
| `make build-public` | Build public app only |
| `make deps` | Download Go dependencies |
| `make clean` | Remove binaries and database |
| `make db-info` | Show database statistics |
| `make test` | Run tests |
| `make version` | Show version info |
| `make quickstart` | Build everything + load sample data |

---

## Docker

A `docker-compose.yml` is included for containerized deployments:

```
docker-compose up -d
```

This starts both services with a shared volume for the SQLite database:

- Admin: [http://localhost:9090](http://localhost:9090)
- Public: [http://localhost:8080](http://localhost:8080)

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `9090` (admin) / `8080` (public) | HTTP listen port |
| `DB_PATH` | `./qr-tracker.db` | Path to the SQLite database file |

---

## Configuration

All configuration is stored in the SQLite database and managed through the admin panel.

### Base URL

The base URL (e.g. `https://found.example.com`) is used to generate QR code links. Set this to wherever your public app is accessible.

### Item Path Prefix

Optional URL path prefix for item pages. For example, setting this to `item` generates URLs like `https://found.example.com/item/KEY-42?sig=abc12345` instead of `https://found.example.com/KEY-42?sig=abc12345`.

### QR Code Settings

| Setting | Description |
|---------|-------------|
| **Size** | QR code image size in pixels |
| **Error Correction** | Low, Medium, High, or Highest — higher levels make codes scannable even when partially damaged |
| **Overlay** | Optionally overlay owner initials or last name on the QR code center |
| **Font Size** | Font size for overlay text (auto-calculated if not set) |
| **Border** | White border width around the QR code in pixels |

### Identifier Prefixes

Define prefixes like `KEY`, `LAPTOP`, or `BAG` to auto-generate sequential item codes. Each prefix maintains its own counter. See [IDENTIFIER_PREFIXES.md](IDENTIFIER_PREFIXES.md) for full details.

---

## Database Schema

TRixie uses SQLite with the following tables:

| Table | Purpose |
|-------|---------|
| `owners` | People who own items (name, email, phone, contact preferences) |
| `items` | Registered items (unique code, description, export status) |
| `item_owners` | Many-to-many link between items and owners |
| `config` | Key-value settings (base URL, signature key, QR defaults) |
| `identifier_prefixes` | Auto-increment prefixes for item codes |

Tables are created automatically on first run.

---

## Adding a New Language

TRixie's public app supports internationalization. To add a language:

1. Open `public-app/i18n.go`
2. Add a new `translationsXX()` function following the existing pattern
3. Register it in the `langMap`:
   ```
   var langMap = map[string]Translations{
       "en": translationsEN(),
       "de": translationsDE(),
       "fr": translationsFR(), // your new language
   }
   ```
4. Rebuild the public app

The app automatically negotiates the best language from the browser's `Accept-Language` header.

---

## Project Structure

```
TRixie/
├── admin-app/              # Admin service (Go)
│   ├── main.go             # Routes, handlers, QR generation, DB logic
│   ├── templates/          # HTML templates (admin UI)
│   └── static/             # Embedded static assets
├── public-app/             # Public service (Go)
│   ├── main.go             # Routes, handlers, signature verification
│   ├── i18n.go             # Internationalization & translations
│   └── templates/          # HTML templates (finder-facing UI)
├── docker-compose.yml      # Multi-service Docker setup
├── Dockerfile.admin        # Admin container build
├── Dockerfile.public       # Public container build
├── Makefile                # Build, run, and utility commands
├── sample_data.sql         # Example seed data
└── IDENTIFIER_PREFIXES.md  # Detailed prefix documentation
```

---

## License

See the repository for license details.
