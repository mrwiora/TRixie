//go:build dynamodb

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoDBStore implements DataStore using a single DynamoDB table.
//
// Single-table design:
//
//	PK                 SK                Attributes
//	CONFIG             signature_key     Value
//	CONFIG             item_path_prefix  Value
//	ITEM#KEY-001       META              ItemID, ItemCode, Description, CreatedAt
//	ITEM#KEY-001       OWNER#1           OwnerID, Name, Email, Phone, PhoneDisplayOptions, CreatedAt
//	ITEMID#42          LOOKUP            ItemCode
type DynamoDBStore struct {
	client    *dynamodb.Client
	tableName string
}

// NewDynamoDBStore creates a DynamoDB-backed store.
func NewDynamoDBStore() (*DynamoDBStore, error) {
	tableName := os.Getenv("DYNAMODB_TABLE")
	if tableName == "" {
		tableName = "TrixieTable"
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := dynamodb.NewFromConfig(cfg)
	log.Printf("DynamoDB table: %s", tableName)
	return &DynamoDBStore{client: client, tableName: tableName}, nil
}

// ---------------------------------------------------------------------------
// Attribute helpers
// ---------------------------------------------------------------------------

func getStringAttr(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key]; ok {
		if s, ok := v.(*types.AttributeValueMemberS); ok {
			return s.Value
		}
	}
	return ""
}

func getIntAttr(item map[string]types.AttributeValue, key string) int {
	if v, ok := item[key]; ok {
		if n, ok := v.(*types.AttributeValueMemberN); ok {
			i, _ := strconv.Atoi(n.Value)
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// DataStore implementation
// ---------------------------------------------------------------------------

func (s *DynamoDBStore) GetConfigValue(key string) (string, error) {
	result, err := s.client.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "CONFIG"},
			"SK": &types.AttributeValueMemberS{Value: key},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get config %q: %w", key, err)
	}
	if result.Item == nil {
		return "", &ErrNotFound{Msg: fmt.Sprintf("config key %q not found", key)}
	}
	return getStringAttr(result.Item, "Value"), nil
}

func (s *DynamoDBStore) GetItemByCode(itemCode string) (*Item, error) {
	result, err := s.client.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "ITEM#" + itemCode},
			"SK": &types.AttributeValueMemberS{Value: "META"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get item %q: %w", itemCode, err)
	}
	if result.Item == nil {
		return nil, &ErrNotFound{Msg: fmt.Sprintf("item %q not found", itemCode)}
	}

	item := &Item{
		ID:          getIntAttr(result.Item, "ItemID"),
		ItemCode:    getStringAttr(result.Item, "ItemCode"),
		Description: getStringAttr(result.Item, "Description"),
		CreatedAt:   getStringAttr(result.Item, "CreatedAt"),
	}
	return item, nil
}

func (s *DynamoDBStore) GetOwnersForItem(itemID int) ([]Owner, error) {
	// Step 1: Resolve ItemID -> ItemCode via the ITEMID#<id> / LOOKUP record.
	lookupResult, err := s.client.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "ITEMID#" + strconv.Itoa(itemID)},
			"SK": &types.AttributeValueMemberS{Value: "LOOKUP"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to look up item code for ID %d: %w", itemID, err)
	}
	if lookupResult.Item == nil {
		return nil, &ErrNotFound{Msg: fmt.Sprintf("item ID %d not found", itemID)}
	}
	itemCode := getStringAttr(lookupResult.Item, "ItemCode")

	// Step 2: Query all OWNER# records under ITEM#<code>.
	result, err := s.client.Query(context.TODO(), &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: "ITEM#" + itemCode},
			":sk": &types.AttributeValueMemberS{Value: "OWNER#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query owners for item %q: %w", itemCode, err)
	}

	var owners []Owner
	for _, rec := range result.Items {
		owners = append(owners, Owner{
			ID:                  getIntAttr(rec, "OwnerID"),
			Name:                getStringAttr(rec, "Name"),
			Email:               getStringAttr(rec, "Email"),
			Phone:               getStringAttr(rec, "Phone"),
			PhoneDisplayOptions: getStringAttr(rec, "PhoneDisplayOptions"),
			CreatedAt:           getStringAttr(rec, "CreatedAt"),
		})
	}

	// Sort by name to match SQLite ORDER BY o.name behaviour.
	sort.Slice(owners, func(i, j int) bool {
		return owners[i].Name < owners[j].Name
	})

	return owners, nil
}

func (s *DynamoDBStore) Ping() error {
	_, err := s.client.DescribeTable(context.TODO(), &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	})
	return err
}
