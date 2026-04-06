// Command trixie-sync reads a TRixie SQLite database and replaces the entire
// contents of the DynamoDB single-table so the public Lambda app can serve
// item lookups. The table is purged first, then freshly populated.
//
// Two modes of operation:
//
//  1. CLI — run locally:
//     trixie-sync -db ./qr-tracker.db -table TrixieTable [-region eu-central-1] [-dry-run]
//
//  2. Lambda — triggered by S3 upload: downloads the .db file from S3 to /tmp,
//     then runs the same sync logic.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Purge — scan the entire table and batch-delete every record
// ---------------------------------------------------------------------------

func purgeTable(client *dynamodb.Client, tableName string, dryRun bool) error {
	ctx := context.TODO()
	log.Printf("Purging all records from %s ...", tableName)

	totalDeleted := 0
	var lastKey map[string]types.AttributeValue

	for {
		// Scan a page of records (only need the keys)
		scanInput := &dynamodb.ScanInput{
			TableName:            aws.String(tableName),
			ProjectionExpression: aws.String("PK, SK"),
			Limit:                aws.Int32(500),
		}
		if lastKey != nil {
			scanInput.ExclusiveStartKey = lastKey
		}

		result, err := client.Scan(ctx, scanInput)
		if err != nil {
			return fmt.Errorf("failed to scan table for purge: %w", err)
		}

		if len(result.Items) == 0 {
			break
		}

		// DynamoDB BatchWriteItem accepts up to 25 items per call
		for i := 0; i < len(result.Items); i += 25 {
			end := i + 25
			if end > len(result.Items) {
				end = len(result.Items)
			}
			batch := result.Items[i:end]

			var requests []types.WriteRequest
			for _, item := range batch {
				requests = append(requests, types.WriteRequest{
					DeleteRequest: &types.DeleteRequest{
						Key: map[string]types.AttributeValue{
							"PK": item["PK"],
							"SK": item["SK"],
						},
					},
				})
			}

			if dryRun {
				log.Printf("  [dry-run] would delete %d records", len(requests))
			} else {
				_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
					RequestItems: map[string][]types.WriteRequest{
						tableName: requests,
					},
				})
				if err != nil {
					return fmt.Errorf("failed to batch-delete: %w", err)
				}
			}
			totalDeleted += len(requests)
		}

		lastKey = result.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}

	log.Printf("Purged %d records from %s", totalDeleted, tableName)
	return nil
}

// ---------------------------------------------------------------------------
// Core sync logic — backend-agnostic
// ---------------------------------------------------------------------------

func syncDatabase(db *sql.DB, client *dynamodb.Client, tableName string, dryRun bool) error {
	ctx := context.TODO()

	// ── Purge existing data ────────────────────────────────────────────
	if err := purgeTable(client, tableName, dryRun); err != nil {
		return fmt.Errorf("purge failed: %w", err)
	}

	// ── Sync config ────────────────────────────────────────────────────
	configRows, err := db.Query("SELECT key, value FROM config")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	configCount := 0
	for configRows.Next() {
		var key, value string
		if err := configRows.Scan(&key, &value); err != nil {
			return fmt.Errorf("failed to scan config row: %w", err)
		}
		item := map[string]types.AttributeValue{
			"PK":    &types.AttributeValueMemberS{Value: "CONFIG"},
			"SK":    &types.AttributeValueMemberS{Value: key},
			"Value": &types.AttributeValueMemberS{Value: value},
		}
		if dryRun {
			log.Printf("  [dry-run] PUT CONFIG / %s", key)
		} else {
			if _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableName),
				Item:      item,
			}); err != nil {
				return fmt.Errorf("failed to put config %q: %w", key, err)
			}
		}
		configCount++
	}
	configRows.Close()
	log.Printf("Synced %d config entries", configCount)

	// ── Read all items ─────────────────────────────────────────────────
	itemRows, err := db.Query("SELECT id, item_code, description, created_at FROM items")
	if err != nil {
		return fmt.Errorf("failed to read items: %w", err)
	}

	type itemRecord struct {
		id          int
		itemCode    string
		description string
		createdAt   string
	}
	var items []itemRecord
	for itemRows.Next() {
		var r itemRecord
		if err := itemRows.Scan(&r.id, &r.itemCode, &r.description, &r.createdAt); err != nil {
			return fmt.Errorf("failed to scan item row: %w", err)
		}
		items = append(items, r)
	}
	itemRows.Close()

	// ── Write items + owners ───────────────────────────────────────────
	itemCount := 0
	ownerCount := 0
	for _, it := range items {
		pk := "ITEM#" + it.itemCode

		// META record
		metaItem := map[string]types.AttributeValue{
			"PK":          &types.AttributeValueMemberS{Value: pk},
			"SK":          &types.AttributeValueMemberS{Value: "META"},
			"ItemID":      &types.AttributeValueMemberN{Value: strconv.Itoa(it.id)},
			"ItemCode":    &types.AttributeValueMemberS{Value: it.itemCode},
			"Description": &types.AttributeValueMemberS{Value: it.description},
			"CreatedAt":   &types.AttributeValueMemberS{Value: it.createdAt},
		}
		if dryRun {
			log.Printf("  [dry-run] PUT %s / META", pk)
		} else {
			if _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableName),
				Item:      metaItem,
			}); err != nil {
				return fmt.Errorf("failed to put item %q: %w", it.itemCode, err)
			}
		}
		itemCount++

		// ITEMID# lookup record
		lookupItem := map[string]types.AttributeValue{
			"PK":       &types.AttributeValueMemberS{Value: "ITEMID#" + strconv.Itoa(it.id)},
			"SK":       &types.AttributeValueMemberS{Value: "LOOKUP"},
			"ItemCode": &types.AttributeValueMemberS{Value: it.itemCode},
		}
		if dryRun {
			log.Printf("  [dry-run] PUT ITEMID#%d / LOOKUP", it.id)
		} else {
			if _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableName),
				Item:      lookupItem,
			}); err != nil {
				return fmt.Errorf("failed to put item lookup for ID %d: %w", it.id, err)
			}
		}

		// Owner records
		ownerRows, err := db.Query(`
			SELECT o.id, o.name, o.email, o.phone, o.phone_display_options, o.created_at
			FROM owners o
			INNER JOIN item_owners io ON o.id = io.owner_id
			WHERE io.item_id = ?
			ORDER BY o.name
		`, it.id)
		if err != nil {
			return fmt.Errorf("failed to read owners for item %q: %w", it.itemCode, err)
		}
		for ownerRows.Next() {
			var oid int
			var name, email, phone, phoneOpts, createdAt string
			if err := ownerRows.Scan(&oid, &name, &email, &phone, &phoneOpts, &createdAt); err != nil {
				return fmt.Errorf("failed to scan owner row: %w", err)
			}
			ownerItem := map[string]types.AttributeValue{
				"PK":                  &types.AttributeValueMemberS{Value: pk},
				"SK":                  &types.AttributeValueMemberS{Value: "OWNER#" + strconv.Itoa(oid)},
				"OwnerID":             &types.AttributeValueMemberN{Value: strconv.Itoa(oid)},
				"Name":                &types.AttributeValueMemberS{Value: name},
				"Email":               &types.AttributeValueMemberS{Value: email},
				"Phone":               &types.AttributeValueMemberS{Value: phone},
				"PhoneDisplayOptions": &types.AttributeValueMemberS{Value: phoneOpts},
				"CreatedAt":           &types.AttributeValueMemberS{Value: createdAt},
			}
			if dryRun {
				log.Printf("  [dry-run] PUT %s / OWNER#%d (%s)", pk, oid, name)
			} else {
				if _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
					TableName: aws.String(tableName),
					Item:      ownerItem,
				}); err != nil {
					return fmt.Errorf("failed to put owner %d for item %q: %w", oid, it.itemCode, err)
				}
			}
			ownerCount++
		}
		ownerRows.Close()
	}

	total := configCount + itemCount*2 + ownerCount
	log.Printf("Sync complete: %d config, %d items, %d owner-links (%d total records)", configCount, itemCount, ownerCount, total)
	return nil
}

// ---------------------------------------------------------------------------
// Lambda handler — triggered by S3 upload
// ---------------------------------------------------------------------------

func lambdaHandler(ctx context.Context, s3Event events.S3Event) error {
	tableName := os.Getenv("DYNAMODB_TABLE")
	if tableName == "" {
		tableName = "TrixieTable"
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	s3Client := s3.NewFromConfig(cfg)

	for _, record := range s3Event.Records {
		bucket := record.S3.Bucket.Name
		key := record.S3.Object.Key
		log.Printf("Processing s3://%s/%s", bucket, key)

		// Download the SQLite DB to /tmp
		result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("failed to download s3://%s/%s: %w", bucket, key, err)
		}
		defer result.Body.Close()

		tmpFile := "/tmp/qr-tracker.db"
		f, err := os.Create(tmpFile)
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		if _, err := io.Copy(f, result.Body); err != nil {
			f.Close()
			return fmt.Errorf("failed to write temp file: %w", err)
		}
		f.Close()
		log.Printf("Downloaded %s to %s", key, tmpFile)

		// Open and sync
		db, err := sql.Open("sqlite", "file:"+tmpFile+"?mode=ro")
		if err != nil {
			return fmt.Errorf("failed to open downloaded DB: %w", err)
		}
		if err := syncDatabase(db, ddbClient, tableName, false); err != nil {
			db.Close()
			return err
		}
		db.Close()

		// Clean up
		os.Remove(tmpFile)
	}

	return nil
}

// ---------------------------------------------------------------------------
// CLI entry point
// ---------------------------------------------------------------------------

func main() {
	// Detect Lambda environment
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(lambdaHandler)
		return
	}

	// CLI mode
	dbPath := flag.String("db", "./qr-tracker.db", "Path to the SQLite database")
	tableName := flag.String("table", "TrixieTable", "DynamoDB table name")
	region := flag.String("region", "", "AWS region (uses default chain if empty)")
	dryRun := flag.Bool("dry-run", false, "Print what would be written without writing")
	flag.Parse()

	// Open SQLite
	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("Database not accessible: %v", err)
	}
	log.Printf("Opened SQLite database: %s", *dbPath)

	// Connect to DynamoDB (unless dry-run)
	var client *dynamodb.Client
	if !*dryRun {
		opts := []func(*awsconfig.LoadOptions) error{}
		if *region != "" {
			opts = append(opts, awsconfig.WithRegion(*region))
		}
		cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), opts...)
		if err != nil {
			log.Fatalf("Failed to load AWS config: %v", err)
		}
		client = dynamodb.NewFromConfig(cfg)
		log.Printf("Target DynamoDB table: %s", *tableName)
	} else {
		log.Println("DRY RUN mode — no writes will be performed")
	}

	if err := syncDatabase(db, client, *tableName, *dryRun); err != nil {
		log.Fatalf("Sync failed: %v", err)
	}
}
