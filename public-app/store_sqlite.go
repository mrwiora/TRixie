//go:build sqlite || !dynamodb

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore implements DataStore using a read-only SQLite connection.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens the SQLite database in read-only mode.
func NewSQLiteStore() (*SQLiteStore, error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./qr-tracker.db"
	}

	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database not accessible: %w", err)
	}

	log.Printf("Database: %s (read-only)", dbPath)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) GetConfigValue(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", &ErrNotFound{Msg: fmt.Sprintf("config key %q not found", key)}
	}
	if err != nil {
		return "", fmt.Errorf("failed to get config %q: %w", key, err)
	}
	return value, nil
}

func (s *SQLiteStore) GetItemByCode(itemCode string) (*Item, error) {
	item := &Item{}
	err := s.db.QueryRow(`
		SELECT id, item_code, description, created_at
		FROM items
		WHERE item_code = ?
	`, itemCode).Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, &ErrNotFound{Msg: fmt.Sprintf("item %q not found", itemCode)}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get item %q: %w", itemCode, err)
	}
	return item, nil
}

func (s *SQLiteStore) GetOwnersForItem(itemID int) ([]Owner, error) {
	rows, err := s.db.Query(`
		SELECT o.id, o.name, o.email, o.phone, o.phone_display_options, o.created_at
		FROM owners o
		INNER JOIN item_owners io ON o.id = io.owner_id
		WHERE io.item_id = ?
		ORDER BY o.name
	`, itemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get owners for item %d: %w", itemID, err)
	}
	defer rows.Close()

	var owners []Owner
	for rows.Next() {
		var owner Owner
		if err := rows.Scan(&owner.ID, &owner.Name, &owner.Email, &owner.Phone, &owner.PhoneDisplayOptions, &owner.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan owner: %w", err)
		}
		owners = append(owners, owner)
	}
	return owners, nil
}

func (s *SQLiteStore) Ping() error {
	return s.db.Ping()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
