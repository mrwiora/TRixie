package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Version information - injected at build time via ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

//go:embed templates/*
var templatesFS embed.FS

var db *sql.DB
var templates *template.Template
var signatureKey []byte
var itemPathPrefix string

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

type Item struct {
	ID          int
	ItemCode    string
	Description string
	CreatedAt   string
	Owners      []Owner
}

func main() {
	// Get database path from environment or use default
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./qr-tracker.db"
	}

	// Initialize database (read-only mode)
	var err error
	db, err = sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(); err != nil {
		log.Fatal("Database not accessible:", err)
	}

	// Load signature key and item path prefix
	if err := loadSignatureKey(); err != nil {
		log.Fatal("Failed to load signature key:", err)
	}
	if err := loadItemPathPrefix(); err != nil {
		log.Fatal("Failed to load item path prefix:", err)
	}

	// Parse templates
	templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

	// Routes
	http.HandleFunc("/", handleRequest)
	http.HandleFunc("/health", handleHealth)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Public TRixie starting on http://localhost:%s", port)
	log.Printf("Version: %s, Commit: %s, Built: %s", Version, GitCommit, BuildTime)
	log.Printf("Database: %s (read-only)", dbPath)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func loadSignatureKey() error {
	var keyHex string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'signature_key'").Scan(&keyHex)
	if err != nil {
		return err
	}

	signatureKey, err = hex.DecodeString(keyHex)
	if err != nil {
		return err
	}

	log.Printf("Loaded signature key for verification")
	return nil
}

func loadItemPathPrefix() error {
	var prefix string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'item_path_prefix'").Scan(&prefix)
	if err == sql.ErrNoRows {
		itemPathPrefix = "" // Default to no prefix
		log.Printf("Item path prefix not configured, using root path")
		return nil
	}
	if err != nil {
		return err
	}
	itemPathPrefix = prefix
	if itemPathPrefix != "" {
		log.Printf("Loaded item path prefix: /%s/", itemPathPrefix)
	} else {
		log.Printf("Item path prefix is empty, using root path")
	}
	return nil
}

func verifySignature(itemCode, signature string) bool {
	h := hmac.New(sha256.New, signatureKey)
	h.Write([]byte(itemCode))
	fullHash := hex.EncodeToString(h.Sum(nil))
	// Use only first 8 characters for shorter URLs
	expected := fullHash[:8]
	return hmac.Equal([]byte(expected), []byte(signature))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	// Home page
	if r.URL.Path == "/" {
		handleHome(w, r)
		return
	}

	// Extract item code from URL path
	var itemCode string
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Handle both prefixed and non-prefixed paths
	if itemPathPrefix != "" {
		// If prefix is configured, try to match it
		expectedPrefix := itemPathPrefix + "/"
		if strings.HasPrefix(path, expectedPrefix) {
			itemCode = strings.TrimPrefix(path, expectedPrefix)
		} else {
			// Also support non-prefixed path for backward compatibility
			itemCode = path
		}
	} else {
		// No prefix configured, use the full path
		itemCode = path
	}

	if itemCode == "" {
		http.NotFound(w, r)
		return
	}

	handleItem(w, r, itemCode)
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	t := detectLanguage(r)
	data := map[string]interface{}{
		"Title":   "TRixie",
		"Version": Version,
		"T":       t,
		"Lang":    t.Code,
	}

	if err := templates.ExecuteTemplate(w, "home.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleItem(w http.ResponseWriter, r *http.Request, itemCode string) {
	t := detectLanguage(r)

	errData := map[string]interface{}{
		"T":       t,
		"Lang":    t.Code,
		"Version": Version,
	}

	// Verify signature
	signature := r.URL.Query().Get("sig")
	if signature == "" {
		w.WriteHeader(http.StatusForbidden)
		if err := templates.ExecuteTemplate(w, "error.html", errData); err != nil {
			http.Error(w, "Access denied: Missing signature", http.StatusForbidden)
		}
		return
	}

	if !verifySignature(itemCode, signature) {
		log.Printf("Invalid signature for item: %s", itemCode)
		w.WriteHeader(http.StatusForbidden)
		if err := templates.ExecuteTemplate(w, "error.html", errData); err != nil {
			http.Error(w, "Access denied: Invalid signature", http.StatusForbidden)
		}
		return
	}

	// Get item from database
	item, err := getItemByCode(itemCode)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Item not found", http.StatusNotFound)
			return
		}
		log.Printf("Error fetching item %s: %v", itemCode, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Get owners for this item
	owners, err := getOwnersForItem(item.ID)
	if err != nil {
		log.Printf("Error fetching owners for item %s: %v", itemCode, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	item.Owners = owners

	// Pre-compute URL-encoded message strings with the item code baked in
	msgs := BuildMessageStrings(t, itemCode)

	data := map[string]interface{}{
		"Item":         item,
		"Owners":       owners,
		"Version":      Version,
		"T":            t,
		"Lang":         t.Code,
		"EmailSubject": msgs.EmailSubject,
		"EmailBody":    msgs.EmailBody,
		"SMSBody":      msgs.SMSBody,
		"WhatsAppBody": msgs.WhatsAppBody,
	}

	if err := templates.ExecuteTemplate(w, "item.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	if err := db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Database unavailable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Database functions
func getItemByCode(itemCode string) (*Item, error) {
	item := &Item{}
	err := db.QueryRow(`
		SELECT id, item_code, description, created_at
		FROM items
		WHERE item_code = ?
	`, itemCode).Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt)

	if err != nil {
		return nil, err
	}
	return item, nil
}

func getOwnersForItem(itemID int) ([]Owner, error) {
	rows, err := db.Query(`
		SELECT o.id, o.name, o.email, o.phone, o.phone_display_options, o.created_at
		FROM owners o
		INNER JOIN item_owners io ON o.id = io.owner_id
		WHERE io.item_id = ?
		ORDER BY o.name
	`, itemID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var owners []Owner
	for rows.Next() {
		var owner Owner
		if err := rows.Scan(&owner.ID, &owner.Name, &owner.Email, &owner.Phone, &owner.PhoneDisplayOptions, &owner.CreatedAt); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}

	return owners, nil
}
