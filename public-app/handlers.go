package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"strings"
)

// Version information - injected at build time via ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

//go:embed templates/*
var templatesFS embed.FS

var templates *template.Template
var signatureKey []byte
var itemPathPrefix string
var dataStore DataStore

// initHandlers initialises shared state from the given store and returns
// a ready-to-use ServeMux. Both the on-premise main and the Lambda main
// call this so the routing logic is defined exactly once.
func initHandlers(store DataStore) *http.ServeMux {
	dataStore = store

	// Load signature key
	if err := loadSignatureKey(store); err != nil {
		log.Fatal("Failed to load signature key:", err)
	}

	// Load item path prefix
	if err := loadItemPathPrefix(store); err != nil {
		log.Fatal("Failed to load item path prefix:", err)
	}

	// Parse templates
	templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRequest)
	mux.HandleFunc("/health", handleHealth)
	return mux
}

func loadSignatureKey(store DataStore) error {
	keyHex, err := store.GetConfigValue("signature_key")
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

func loadItemPathPrefix(store DataStore) error {
	prefix, err := store.GetConfigValue("item_path_prefix")
	if err != nil {
		if IsNotFound(err) {
			itemPathPrefix = ""
			log.Printf("Item path prefix not configured, using root path")
			return nil
		}
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

	// Get item from data store
	item, err := dataStore.GetItemByCode(itemCode)
	if err != nil {
		if IsNotFound(err) {
			http.Error(w, "Item not found", http.StatusNotFound)
			return
		}
		log.Printf("Error fetching item %s: %v", itemCode, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Get owners for this item
	owners, err := dataStore.GetOwnersForItem(item.ID)
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
	// Check data store connection
	if err := dataStore.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Database unavailable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
