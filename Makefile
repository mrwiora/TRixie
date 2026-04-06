.PHONY: help build clean run-public run-admin run-both deps test install \
        build-lambda build-sync sync sync-dry-run \
        aws-package aws-deploy aws-update aws-delete aws-status aws-outputs

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
	@echo "  make build         - Build both applications (on-premise, SQLite)"
	@echo "  make build-public  - Build public app only (on-premise, SQLite)"
	@echo "  make build-admin   - Build admin app only"
	@echo "  make build-lambda  - Build public app for AWS Lambda (DynamoDB)"
	@echo "  make build-sync    - Build the sync tool CLI"
	@echo "  make clean         - Remove build artifacts and database"
	@echo "  make version       - Show version information"
	@echo ""
	@echo "Running:"
	@echo "  make run-public    - Run public app (port 8080)"
	@echo "  make run-admin     - Run admin app (port 9090)"
	@echo "  make run           - Show how to run both apps"
	@echo ""
	@echo "AWS Deployment (requires aws cli v2):"
	@echo "  make aws-package   - Build binaries, create ZIPs, upload to S3"
	@echo "  make aws-deploy    - First-time deploy (create stack)"
	@echo "  make aws-update    - Update existing stack + Lambda code"
	@echo "  make aws-status    - Show stack status"
	@echo "  make aws-outputs   - Show stack outputs (API URL, bucket name, etc.)"
	@echo "  make aws-delete    - Tear down the entire stack"
	@echo ""
	@echo "Data Sync:"
	@echo "  make sync          - Sync local SQLite DB to DynamoDB (CLI, direct)"
	@echo "  make sync-s3       - Upload .db to S3 (triggers Lambda sync)"
	@echo "  make sync-dry-run  - Preview what sync would write"
	@echo ""
	@echo "Development:"
	@echo "  make deps          - Download dependencies for all apps"
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

# Build public app (on-premise / SQLite — default, no build tags needed)
build-public:
	@echo "🔨 Building public app (SQLite)..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd public-app && CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-public .

# Build public app for AWS Lambda (DynamoDB — no CGO required)
build-lambda:
	@echo "🔨 Building public app for Lambda (DynamoDB)..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd public-app && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags dynamodb -ldflags="$(LDFLAGS)" -o ../bootstrap .
	@echo "✅ Lambda binary built: bootstrap"

# Build sync tool CLI (pure Go — no CGO needed thanks to modernc.org/sqlite)
build-sync:
	@echo "🔨 Building sync tool (linux/arm64)..."
	cd sync-tool && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ../trixie-sync .
	@echo "✅ Sync tool built: trixie-sync"

# Build admin app
build-admin:
	@echo "🔨 Building admin app..."
	@echo "Version: $(VERSION_INFO), Commit: $(GIT_COMMIT)$(GIT_DIRTY)"
	cd admin-app && go build -ldflags="$(LDFLAGS)" -o ../qr-tracker-admin

# Download dependencies for all apps
deps:
	@echo "📦 Downloading dependencies..."
	cd public-app && go mod download && go mod tidy
	cd admin-app && go mod download && go mod tidy
	cd sync-tool && go mod download && go mod tidy
	@echo "✅ Dependencies installed"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -f qr-tracker-public qr-tracker-admin bootstrap trixie-sync
	rm -f qr-tracker.db qr-tracker.db-shm qr-tracker.db-wal
	rm -rf .aws-sam/ .build/
	@echo "✅ Cleaned"

# Run public app (on-premise / SQLite)
run-public:
	@echo "🚀 Starting public app on port 8080..."
	@echo "📁 Database: qr-tracker.db"
	cd public-app && PORT=8080 DB_PATH=$(CURDIR)/qr-tracker.db go run .

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

# ---------------------------------------------------------------------------
# AWS deployment targets (pure aws cli v2 — no SAM CLI required)
# ---------------------------------------------------------------------------

# Configuration — override on the command line or export in your shell:
#   make aws-deploy AWS_STACK=my-trixie AWS_REGION=us-east-1
AWS_STACK  ?= trixie
AWS_REGION ?= eu-central-1
# The S3 bucket that holds Lambda deployment ZIPs.
# Created automatically by `aws-package` if it doesn't exist.
AWS_DEPLOY_BUCKET ?= trixie-deploy-$(shell aws sts get-caller-identity --query Account --output text 2>/dev/null || echo "UNKNOWN")

# Build Lambda binaries and upload deployment ZIPs to S3
aws-package: build-lambda build-sync
	@echo "📦 Packaging Lambda ZIPs..."
	@mkdir -p .build
	@# Public Lambda — pure Go, no CGO
	cd .build && cp ../bootstrap . && zip trixie-public.zip bootstrap && rm bootstrap
	@# Sync Lambda — needs CGO (SQLite), already built as trixie-sync
	cd .build && cp ../trixie-sync bootstrap && zip trixie-sync.zip bootstrap && rm bootstrap
	@echo "☁️  Ensuring S3 bucket exists: $(AWS_DEPLOY_BUCKET)"
	@aws s3api head-bucket --bucket $(AWS_DEPLOY_BUCKET) --region $(AWS_REGION) 2>/dev/null || \
		aws s3api create-bucket --bucket $(AWS_DEPLOY_BUCKET) --region $(AWS_REGION) \
			--create-bucket-configuration LocationConstraint=$(AWS_REGION)
	@echo "☁️  Uploading ZIPs to s3://$(AWS_DEPLOY_BUCKET)/lambda/"
	aws s3 cp .build/trixie-public.zip s3://$(AWS_DEPLOY_BUCKET)/lambda/trixie-public.zip --region $(AWS_REGION)
	aws s3 cp .build/trixie-sync.zip   s3://$(AWS_DEPLOY_BUCKET)/lambda/trixie-sync.zip   --region $(AWS_REGION)
	@echo "✅ Packaging complete"

# First-time deploy — create the CloudFormation stack
aws-deploy: aws-package
	@echo "☁️  Creating CloudFormation stack: $(AWS_STACK) in $(AWS_REGION)"
	aws cloudformation create-stack \
		--stack-name $(AWS_STACK) \
		--template-body file://sam-template.yaml \
		--capabilities CAPABILITY_NAMED_IAM \
		--region $(AWS_REGION) \
		--parameters \
			ParameterKey=PublicLambdaS3Bucket,ParameterValue=$(AWS_DEPLOY_BUCKET) \
			ParameterKey=PublicLambdaS3Key,ParameterValue=lambda/trixie-public.zip \
			ParameterKey=SyncLambdaS3Key,ParameterValue=lambda/trixie-sync.zip
	@echo "⏳ Waiting for stack creation to complete..."
	aws cloudformation wait stack-create-complete \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION)
	@echo "✅ Stack created successfully!"
	@$(MAKE) aws-outputs

# Update existing stack (template changes + fresh Lambda code)
aws-update: aws-package
	@echo "☁️  Updating CloudFormation stack: $(AWS_STACK)"
	aws cloudformation update-stack \
		--stack-name $(AWS_STACK) \
		--template-body file://sam-template.yaml \
		--capabilities CAPABILITY_NAMED_IAM \
		--region $(AWS_REGION) \
		--parameters \
			ParameterKey=PublicLambdaS3Bucket,ParameterValue=$(AWS_DEPLOY_BUCKET) \
			ParameterKey=PublicLambdaS3Key,ParameterValue=lambda/trixie-public.zip \
			ParameterKey=SyncLambdaS3Key,ParameterValue=lambda/trixie-sync.zip
	@echo "⏳ Waiting for stack update to complete..."
	aws cloudformation wait stack-update-complete \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION)
	@echo "🔄 Updating Lambda function code (public)..."
	@PUBLIC_FN=$$(aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) --region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[?OutputKey==`PublicFunctionName`].OutputValue' \
		--output text) && \
	aws lambda update-function-code \
		--function-name "$$PUBLIC_FN" \
		--s3-bucket $(AWS_DEPLOY_BUCKET) \
		--s3-key lambda/trixie-public.zip \
		--architectures arm64 \
		--region $(AWS_REGION)
	@echo "🔄 Updating Lambda function code (sync)..."
	@SYNC_FN=$$(aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) --region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[?OutputKey==`SyncFunctionName`].OutputValue' \
		--output text) && \
	aws lambda update-function-code \
		--function-name "$$SYNC_FN" \
		--s3-bucket $(AWS_DEPLOY_BUCKET) \
		--s3-key lambda/trixie-sync.zip \
		--architectures arm64 \
		--region $(AWS_REGION)
	@echo "✅ Stack updated successfully!"
	@$(MAKE) aws-outputs

# Show stack status
aws-status:
	@aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION) \
		--query 'Stacks[0].{Status:StackStatus,Created:CreationTime,Updated:LastUpdatedTime}' \
		--output table 2>/dev/null || echo "❌ Stack '$(AWS_STACK)' not found in $(AWS_REGION)"

# Show stack outputs (API URL, table name, sync bucket, etc.)
aws-outputs:
	@echo "=== Stack Outputs ==="
	@aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[*].{Key:OutputKey,Value:OutputValue}' \
		--output table 2>/dev/null || echo "❌ Stack '$(AWS_STACK)' not found"

# Tear down the entire stack (removes all AWS resources)
aws-delete:
	@echo "⚠️  This will delete ALL resources in stack '$(AWS_STACK)'!"
	@echo "    (DynamoDB table, Lambda functions, API Gateway, S3 sync bucket, log groups)"
	@echo ""
	@read -p "Type 'yes' to confirm: " confirm && [ "$$confirm" = "yes" ] || (echo "Aborted."; exit 1)
	@echo "🗑  Emptying sync bucket..."
	@BUCKET=$$(aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) --region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[?OutputKey==`SyncBucketName`].OutputValue' \
		--output text 2>/dev/null) && \
	if [ -n "$$BUCKET" ]; then \
		aws s3 rm "s3://$$BUCKET/" --recursive --region $(AWS_REGION) 2>/dev/null || true; \
	fi
	@echo "🗑  Deleting CloudFormation stack: $(AWS_STACK)"
	aws cloudformation delete-stack \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION)
	@echo "⏳ Waiting for stack deletion..."
	aws cloudformation wait stack-delete-complete \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION)
	@echo "✅ Stack deleted"

# ---------------------------------------------------------------------------
# Data sync targets
# ---------------------------------------------------------------------------

# Sync local SQLite DB to DynamoDB directly (CLI tool, no Lambda involved)
sync: build-sync
	@if [ ! -f qr-tracker.db ]; then \
		echo "❌ qr-tracker.db not found. Run 'make run-admin' first."; \
		exit 1; \
	fi
	@TABLE=$$(aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) --region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[?OutputKey==`TrixieTableName`].OutputValue' \
		--output text 2>/dev/null) && \
	if [ -z "$$TABLE" ]; then \
		echo "❌ Could not find TrixieTableName in stack '$(AWS_STACK)'. Is the stack deployed?"; \
		exit 1; \
	fi && \
	echo "☁️  Syncing to DynamoDB table: $$TABLE" && \
	./trixie-sync -db qr-tracker.db -table "$$TABLE" -region $(AWS_REGION)

# Upload the .db file to S3 — triggers the sync Lambda automatically
sync-s3:
	@if [ ! -f qr-tracker.db ]; then \
		echo "❌ qr-tracker.db not found. Run 'make run-admin' first."; \
		exit 1; \
	fi
	@BUCKET=$$(aws cloudformation describe-stacks \
		--stack-name $(AWS_STACK) \
		--region $(AWS_REGION) \
		--query 'Stacks[0].Outputs[?OutputKey==`SyncBucketName`].OutputValue' \
		--output text 2>/dev/null) && \
	if [ -z "$$BUCKET" ]; then \
		echo "❌ Could not find SyncBucketName in stack '$(AWS_STACK)'. Is the stack deployed?"; \
		exit 1; \
	fi && \
	echo "☁️  Uploading qr-tracker.db to s3://$$BUCKET/" && \
	aws s3 cp qr-tracker.db "s3://$$BUCKET/qr-tracker.db" --region $(AWS_REGION) && \
	echo "✅ Upload complete — sync Lambda will process it automatically"

# Preview what sync would write (no actual DynamoDB writes)
sync-dry-run: build-sync
	@if [ ! -f qr-tracker.db ]; then \
		echo "❌ qr-tracker.db not found. Run 'make run-admin' first."; \
		exit 1; \
	fi
	./trixie-sync -db qr-tracker.db -dry-run -table $(AWS_STACK)-table
