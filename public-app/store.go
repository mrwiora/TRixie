package main

import "strings"

// DataStore abstracts the persistence layer so the public app can run against
// different backends (SQLite for on-premise, DynamoDB for AWS Lambda, etc.).
type DataStore interface {
	// GetConfigValue retrieves a single configuration value by key.
	// Returns ("", ErrNotFound) when the key does not exist.
	GetConfigValue(key string) (string, error)

	// GetItemByCode looks up an item by its unique item_code.
	// Returns (nil, ErrNotFound) when no item matches.
	GetItemByCode(itemCode string) (*Item, error)

	// GetOwnersForItem returns all owners linked to the given item ID.
	GetOwnersForItem(itemID int) ([]Owner, error)

	// Ping checks that the underlying data source is reachable.
	Ping() error
}

// ErrNotFound is returned when a requested record does not exist.
type ErrNotFound struct{ Msg string }

func (e *ErrNotFound) Error() string { return e.Msg }

// IsNotFound checks whether err is an ErrNotFound.
func IsNotFound(err error) bool {
	_, ok := err.(*ErrNotFound)
	return ok
}

// ---------------------------------------------------------------------------
// Domain models
// ---------------------------------------------------------------------------

// Owner represents a person who owns one or more tracked items.
type Owner struct {
	ID                  int
	Name                string
	Email               string
	Phone               string
	PhoneDisplayOptions string
	CreatedAt           string
}

// Helper methods to check display options

func (o *Owner) ShowCall() bool {
	return strings.Contains(o.PhoneDisplayOptions, "call")
}

func (o *Owner) ShowSMS() bool {
	return strings.Contains(o.PhoneDisplayOptions, "sms")
}

func (o *Owner) ShowWhatsApp() bool {
	return strings.Contains(o.PhoneDisplayOptions, "whatsapp")
}

// Item represents a tracked item identified by a unique item code.
type Item struct {
	ID          int
	ItemCode    string
	Description string
	CreatedAt   string
	Owners      []Owner
}
