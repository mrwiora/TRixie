# TRixie - Public Application

This is the **public-facing** application that displays item information when QR codes are scanned. It operates in **read-only mode** and only shows data - no modifications are allowed.

## Purpose

When someone finds a lost item with a QR code sticker:
1. They scan the QR code
2. Their browser opens this application
3. They see the owner's contact information
4. They can contact the owner via email, phone, or WhatsApp

## Features

- ✅ **Read-Only Database Access** - Cannot modify data
- ✅ **Item Display** - Shows item details and owner contact info
- ✅ **Mobile-Friendly** - Responsive design for all devices
- ✅ **Fast & Lightweight** - Minimal dependencies
- ✅ **Health Check Endpoint** - `/health` for monitoring

## Installation

### Prerequisites

- Go 1.21+
- GCC (for SQLite compilation)
- Access to the shared `qr_tracker.db` database

### Quick Start

```bash
# Navigate to public app directory
cd public-app

# Download dependencies
go mod download

# Run the application
go run main.go
```

The server starts on **http://localhost:8080** by default.

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `DB_PATH` | Path to SQLite database | `../qr_tracker.db` |

### Example Usage

```bash
# Custom port
PORT=3000 go run main.go

# Custom database path
DB_PATH=/var/data/qr_tracker.db go run main.go

# Both
PORT=3000 DB_PATH=/var/data/qr_tracker.db go run main.go
```

## Building

### Build Binary

```bash
go build -o qr-tracker-public
```

### Build for Production (Optimized)

```bash
go build -ldflags="-s -w" -o qr-tracker-public
```

### Cross-Platform Build

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o qr-tracker-public-linux

# macOS
GOOS=darwin GOARCH=amd64 go build -o qr-tracker-public-darwin

# Windows
GOOS=windows GOARCH=amd64 go build -o qr-tracker-public.exe
```

## API Endpoints

### Public Routes

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/` | Home page with information |
| GET | `/item/{code}` | Item details page (QR destination) |
| GET | `/health` | Health check endpoint |

### Example URLs

```
http://localhost:8080/
http://localhost:8080/item/KEY-001
http://localhost:8080/item/BAG-2024
http://localhost:8080/health
```

## Deployment

### Option 1: Standalone Binary

```bash
# Build
go build -ldflags="-s -w" -o qr-tracker-public

# Run with systemd
sudo nano /etc/systemd/system/qr-tracker-public.service
```

**Service File Example:**
```ini
[Unit]
Description=TRixie Public
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/qr-tracker
Environment="PORT=8080"
Environment="DB_PATH=/opt/qr-tracker/qr_tracker.db"
ExecStart=/opt/qr-tracker/qr-tracker-public
Restart=always

[Install]
WantedBy=multi-user.target
```

### Option 2: Docker

Create `Dockerfile`:

```dockerfile
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o qr-tracker-public

FROM alpine:latest
RUN apk add --no-cache ca-certificates sqlite-libs
WORKDIR /root/
COPY --from=builder /app/qr-tracker-public .
EXPOSE 8080
CMD ["./qr-tracker-public"]
```

Build and run:

```bash
docker build -t qr-tracker-public .
docker run -p 8080:8080 -v /path/to/db:/root qr-tracker-public
```

### Option 3: Behind Reverse Proxy (Recommended)

**Nginx Configuration:**

```nginx
server {
    listen 80;
    server_name items.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Add SSL with Let's Encrypt:

```bash
sudo certbot --nginx -d items.yourdomain.com
```

## Database Setup

This application requires a shared database created by the admin application.

### Database Location

The database should be accessible at the path specified by `DB_PATH` (default: `../qr_tracker.db`).

### Read-Only Mode

The application opens the database in **read-only mode** using:
```go
sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
```

This prevents accidental data modifications.

### Permissions

Ensure the application user has **read** permission:

```bash
chmod 644 qr_tracker.db
chown www-data:www-data qr_tracker.db
```

## Security Considerations

### ✅ Safe for Public Internet

- **Read-Only**: Cannot modify data
- **No Admin Features**: No CRUD operations
- **No Authentication Required**: Designed for public access
- **Limited Surface**: Only two routes (home and item)

### 🔒 Best Practices

1. **Use HTTPS** in production (via reverse proxy)
2. **Rate Limiting** (via reverse proxy or firewall)
3. **DDoS Protection** (Cloudflare, etc.)
4. **Monitor Access** logs regularly
5. **Keep Database Separate** from admin app

### ⚠️ Important

- This app is designed to be public
- Do NOT expose the admin app publicly
- Use separate domains/subdomains:
  - Public: `items.yourdomain.com`
  - Admin: `admin-internal.yourdomain.com` (internal only)

## Monitoring

### Health Check

The `/health` endpoint returns:
- `200 OK` - Application is healthy
- `503 Service Unavailable` - Database connection failed

Use with monitoring tools:

```bash
# Manual check
curl http://localhost:8080/health

# With monitoring (example)
watch -n 30 'curl -sf http://localhost:8080/health || echo "ALERT: Service down!"'
```

### Logging

The application logs to stdout:

```bash
# Run with logging to file
./qr-tracker-public 2>&1 | tee -a app.log

# View logs with timestamps
./qr-tracker-public 2>&1 | ts '[%Y-%m-%d %H:%M:%S]' | tee -a app.log
```

## Performance

### Optimization Tips

1. **Use SSD** for database storage
2. **Enable SQLite WAL mode** (admin app handles this)
3. **Cache-Control Headers** (set via reverse proxy)
4. **CDN** for static assets
5. **Multiple Instances** behind load balancer

### Expected Performance

- **Response Time**: < 50ms (local SSD)
- **Concurrent Users**: 1000+ (with proper setup)
- **Database Size**: Scales to millions of records

## Troubleshooting

### "Database not accessible"

**Cause**: Database file not found or no read permission

**Solution**:
```bash
# Check path
ls -la ../qr_tracker.db

# Fix permissions
chmod 644 ../qr_tracker.db

# Set correct DB_PATH
DB_PATH=/correct/path/qr_tracker.db go run main.go
```

### "Port already in use"

**Solution**:
```bash
# Use different port
PORT=3000 go run main.go

# Or kill existing process
lsof -ti:8080 | xargs kill
```

### "Item not found"

**Cause**: Item code doesn't exist in database

**Solution**: Check admin app to verify item is registered

### "Template not found"

**Cause**: Templates not embedded in binary

**Solution**: Rebuild the binary (templates are embedded at compile time)

## Development

### Local Development

```bash
# Run with auto-reload (requires air)
go install github.com/cosmtrek/air@latest
air

# Or manual restart on changes
go run main.go
```

### Testing

```bash
# Test item endpoint
curl http://localhost:8080/item/KEY-001

# Test health
curl http://localhost:8080/health

# Load test (requires hey)
hey -n 1000 -c 50 http://localhost:8080/item/KEY-001
```

## Integration with Admin App

This app works alongside the admin application:

```
┌─────────────────┐
│   Admin App     │ (Port 9090, Internal Only)
│   Read-Write    │ ──► Creates/Updates data
└────────┬────────┘
         │
         ▼
┌──────────────────┐
│  qr_tracker.db   │ (Shared SQLite Database)
└────────┬─────────┘
         │
         ▼
┌─────────────────┐
│   Public App    │ (Port 8080, Public Internet)
│   Read-Only     │ ──► Displays data
└─────────────────┘
```

### Deployment Architecture

**Recommended Setup:**

1. **Admin App**: Internal network only (VPN or private IP)
2. **Public App**: Public internet with HTTPS
3. **Database**: Shared file, read by public, written by admin

## Support

For issues or questions:
- Check the main project README
- Review the admin app README
- Check application logs

## License

Part of the TRixie project.