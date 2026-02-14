.PHONY: help build clean run-public run-admin run-both deps test install

# Version information from git
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "")
GIT_DIRTY := $(shell git diff --quiet 2>/dev/null || echo "-dirty")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# If we're not on a tag, include the commit in the version
ifneq ($(GIT_TAG),)
	VERSION_INFO := $(GIT_TAG)
else
	VERSION_INFO := $(VERSION)
endif

# Build flags to inject version information
LDFLAGS := -s -w -X main.Version=$(VERSION_INFO) -X main.GitCommit=$(GIT_COMMIT)$(GIT_DIRTY) -X main.BuildTime=$(BUILD_TIME)


# Default target
help:
	@echo "TRixie - Makefile Commands"
	@echo ""
	@echo "Building:"
	@echo "  make build         - Build both applications"
	@echo "  make build-public  - Build public app only"
	@echo "  make build-admin   - Build admin app only"
	@echo "  make clean         - Remove build artifacts and database"
	@echo "  make version       - Show version information"
	@echo ""
	@echo "Running:"
	@echo "  make run-public    - Run public app (port 8080)"
	@echo "  make run-admin     - Run admin app (port 9090)"
	@echo "  make run           - Show how to run both apps"
	@echo ""
	@echo "Development:"
	@echo "  make deps          - Download dependencies for both apps"
	@echo "  make test          - Run tests"
	@echo "  make sample        - Load sample data into database"
	@echo "  make db-info       - Show database information"

# Show version information
version:
	@echo "=== Version Information ==="
	@echo "Version:    $(VERSION_INFO)"
	@echo "Git Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	@echo "Git Tag:    $(if $(GIT_TAG),$(GIT_TAG),(none))"
	@echo "Build Time: $(BUILD_TIME)"

# Build both applications
build: build-public build-admin
	@echo "✅ Both applications built successfully!"
	@ls -lh qr-tracker-public qr-tracker-admin

# Build public app
build-public:
	@echo "🔨 Building public app..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd public-app && go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-public

# Build admin app
build-admin:
	@echo "🔨 Building admin app..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd admin-app && go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-admin

# Download dependencies for both apps
deps:
	@echo "📦 Downloading dependencies..."
	cd public-app && go mod download && go mod tidy
	cd admin-app && go mod download && go mod tidy
	@echo "✅ Dependencies installed"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -f qr-tracker-public qr-tracker-admin
	rm -f qr-tracker.db qr-tracker.db-shm qr-tracker.db-wal
	@echo "✅ Cleaned"

# Run public app
run-public:
	@echo "🚀 Starting public app on port 8080..."
	@echo "📁 Database: qr-tracker.db"
	cd public-app && PORT=8080 DB_PATH=$(CURDIR)/qr-tracker.db go run main.go

# Run admin app
run-admin:
	@echo "🚀 Starting admin app on port 9090..."
	@echo "📁 Database: qr-tracker.db"
	cd admin-app && PORT=9090 DB_PATH=$(CURDIR)/qr-tracker.db go run main.go

# Run both apps (use run-admin and run-public in separate terminals)
run:
	@echo "To run both applications:"
	@echo "  Terminal 1: make run-admin"
	@echo "  Terminal 2: make run-public"
	@echo ""
	@echo "Database location: qr-tracker.db"

# Load sample data
sample:
	@if [ ! -f qr-tracker.db ]; then \
		echo "Database doesn't exist. Starting admin app to create it..."; \
		cd admin-app && timeout 3 DB_PATH=$(CURDIR)/qr-tracker.db go run main.go || true; \
		sleep 1; \
	fi
	@if [ -f qr-tracker.db ]; then \
		sqlite3 qr-tracker.db < sample_data.sql && echo "✅ Sample data loaded!"; \
	else \
		echo "❌ Failed to create database"; \
	fi

# Show database info
db-info:
	@if [ -f qr-tracker.db ]; then \
		echo "=== Database Information ==="; \
		echo ""; \
		echo "Owners:"; \
		sqlite3 qr-tracker.db "SELECT COUNT(*) || ' registered' FROM owners;"; \
		echo ""; \
		echo "Items:"; \
		sqlite3 qr-tracker.db "SELECT COUNT(*) || ' registered' FROM items;"; \
		echo ""; \
		echo "Recent Items:"; \
		sqlite3 qr-tracker.db "SELECT item_code, description FROM items ORDER BY created_at DESC LIMIT 5;"; \
	else \
		echo "❌ Database not found. Run 'make run-admin' first."; \
	fi

# Run tests
test:
	@echo "Running tests..."
	@cd public-app && go test -v ./... || true
	@cd admin-app && go test -v ./... || true

# Install both binaries to GOPATH/bin
install: build
	@echo "📦 Installing binaries..."
	cp qr-tracker-public $(GOPATH)/bin/
	cp qr-tracker-admin $(GOPATH)/bin/
	@echo "✅ Installed to $(GOPATH)/bin/"

# Production builds
build-prod: clean
	@echo "🔨 Building for production..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd public-app && CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-public
	cd admin-app && CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-admin
	@echo "✅ Production builds ready!"

# Quick start - build everything and load sample data
quickstart: build sample
	@echo ""
	@echo "✅ Quick start complete!"
	@echo ""
	@echo "Run applications with:"
	@echo "  make run-admin   (admin on port 9090)"
	@echo "  make run-public  (public on port 8080)"
	@echo ""
	@echo "Database location: qr-tracker.db"
