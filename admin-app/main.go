package main

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	_ "github.com/mattn/go-sqlite3"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"golang.org/x/image/font/gofont/goregular"
)

//go:embed templates/*
var templatesFS embed.FS

// Version information - injected at build time via ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

var db *sql.DB
var templates *template.Template
var signatureKey []byte

type Owner struct {
	ID                  int
	Name                string
	Initials            string
	Email               string
	Phone               string
	PhoneDisplayOptions string
	CreatedAt           string
}

type Item struct {
	ID          int
	ItemCode    string
	Description string
	CreatedAt   string
	Exported    bool
	ExportedAt  string
	Owners      []Owner
}

type IdentifierPrefix struct {
	ID             int
	Prefix         string
	CurrentCounter int
	CreatedAt      string
}

func main() {
	// Get database path from environment or use default
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./qr-tracker.db"
	}

	// Initialize database (read-write mode)
	var err error
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	// Create tables if they don't exist
	if err := createTables(); err != nil {
		log.Fatal("Failed to create tables:", err)
	}

	// Parse templates with custom functions
	funcMap := template.FuncMap{
		"contains": strings.Contains,
	}
	templates = template.Must(template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html"))

	// Routes
	http.HandleFunc("/", handleAdmin)
	http.HandleFunc("/setup", handleSetup)
	http.HandleFunc("/settings", handleSettings)
	http.HandleFunc("/owners", handleOwners)
	http.HandleFunc("/owners/edit", handleEditOwner)
	http.HandleFunc("/items/new", handleNewItem)
	http.HandleFunc("/items", handleItems)
	http.HandleFunc("/items/edit", handleEditItem)
	http.HandleFunc("/prefixes", handlePrefixes)
	http.HandleFunc("/config", handleConfig)
	http.HandleFunc("/api/owners", handleAPIOwners)
	http.HandleFunc("/api/items", handleAPIItems)
	http.HandleFunc("/api/prefixes", handleAPIPrefixes)
	http.HandleFunc("/qr/single/", handleQRSingle)
	http.HandleFunc("/qr/batch", handleQRBatch)
	http.HandleFunc("/qr/unexported", handleQRUnexported)
	http.HandleFunc("/qr/config", handleQRConfig)
	http.HandleFunc("/health", handleHealth)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}

	log.Printf("Admin TRixie starting on http://localhost:%s", port)
	log.Printf("Version: %s, Commit: %s, Built: %s", Version, GitCommit, BuildTime)
	log.Printf("Database: %s", dbPath)
	log.Println("⚠️  WARNING: This is an admin interface. Restrict access in production!")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func createTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS owners (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		initials TEXT,
		email TEXT NOT NULL,
		phone TEXT,
		phone_display_options TEXT DEFAULT 'call,sms,whatsapp',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		item_code TEXT UNIQUE NOT NULL,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		exported INTEGER DEFAULT 0,
		exported_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS item_owners (
		item_id INTEGER NOT NULL,
		owner_id INTEGER NOT NULL,
		PRIMARY KEY (item_id, owner_id),
		FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
		FOREIGN KEY (owner_id) REFERENCES owners(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS identifier_prefixes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		prefix TEXT UNIQUE NOT NULL,
		current_counter INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_item_code ON items(item_code);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Initialize or load signature key
	return initSignatureKey()
}

func initSignatureKey() error {
	// Try to load existing key
	var keyHex string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'signature_key'").Scan(&keyHex)

	if err == sql.ErrNoRows {
		// Generate new key
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("failed to generate signature key: %v", err)
		}

		keyHex = hex.EncodeToString(key)
		_, err = db.Exec("INSERT INTO config (key, value) VALUES ('signature_key', ?)", keyHex)
		if err != nil {
			return fmt.Errorf("failed to store signature key: %v", err)
		}

		signatureKey = key
		log.Printf("Generated new signature key")
	} else if err != nil {
		return fmt.Errorf("failed to load signature key: %v", err)
	} else {
		// Load existing key
		signatureKey, err = hex.DecodeString(keyHex)
		if err != nil {
			return fmt.Errorf("failed to decode signature key: %v", err)
		}
		log.Printf("Loaded existing signature key")
	}

	return nil
}

func getBaseURL() (string, error) {
	var baseURL string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'base_url'").Scan(&baseURL)
	if err == sql.ErrNoRows {
		return "", nil // Not set yet
	}
	return baseURL, err
}

func setBaseURL(baseURL string) error {
	_, err := db.Exec(`
		INSERT INTO config (key, value) VALUES ('base_url', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, baseURL)
	return err
}

func getItemPathPrefix() (string, error) {
	var prefix string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'item_path_prefix'").Scan(&prefix)
	if err == sql.ErrNoRows {
		return "", nil // Default to empty (no prefix)
	}
	return prefix, err
}

func getQRSize() (int, error) {
	var size string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'qr_size'").Scan(&size)
	if err == sql.ErrNoRows {
		return 256, nil // Default size
	}
	if err != nil {
		return 256, err
	}
	var sizeInt int
	fmt.Sscanf(size, "%d", &sizeInt)
	if sizeInt == 0 {
		sizeInt = 256
	}
	return sizeInt, nil
}

func setQRSize(size int) error {
	_, err := db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('qr_size', ?)", fmt.Sprintf("%d", size))
	return err
}

func getQRErrorCorrection() (string, error) {
	var ec string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'qr_error_correction'").Scan(&ec)
	if err == sql.ErrNoRows {
		return "medium", nil // Default error correction
	}
	return ec, err
}

func setQRErrorCorrection(ec string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('qr_error_correction', ?)", ec)
	return err
}

func getQROverlay() (string, error) {
	var overlay string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'qr_overlay'").Scan(&overlay)
	if err == sql.ErrNoRows {
		return "none", nil // Default no overlay
	}
	return overlay, err
}

func setQROverlay(overlay string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('qr_overlay', ?)", overlay)
	return err
}

func getQRFontSize() (int, error) {
	var size string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'qr_font_size'").Scan(&size)
	if err == sql.ErrNoRows {
		return 0, nil // Default 0 means auto-calculate
	}
	if err != nil {
		return 0, err
	}
	var sizeInt int
	fmt.Sscanf(size, "%d", &sizeInt)
	return sizeInt, nil
}

func setQRFontSize(size int) error {
	_, err := db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('qr_font_size', ?)", fmt.Sprintf("%d", size))
	return err
}

func getQRBorder() (int, error) {
	var border string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'qr_border'").Scan(&border)
	if err == sql.ErrNoRows {
		return 16, nil // Default border of 16 pixels
	}
	if err != nil {
		return 16, err
	}
	var borderInt int
	fmt.Sscanf(border, "%d", &borderInt)
	if borderInt < 1 {
		borderInt = 16 // Enforce minimum of 1
	}
	return borderInt, nil
}

func setQRBorder(border int) error {
	_, err := db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('qr_border', ?)", fmt.Sprintf("%d", border))
	return err
}

func setItemPathPrefix(prefix string) error {
	_, err := db.Exec(`
		INSERT INTO config (key, value) VALUES ('item_path_prefix', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, prefix)
	return err
}

func getSignatureKeyHex() string {
	return hex.EncodeToString(signatureKey)
}

func isSetupCompleted() (bool, error) {
	var value string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'setup_completed'").Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func setSetupCompleted() error {
	_, err := db.Exec(`
		INSERT INTO config (key, value) VALUES ('setup_completed', 'true')
		ON CONFLICT(key) DO UPDATE SET value = 'true'
	`)
	return err
}

func generateSignature(itemCode string) string {
	h := hmac.New(sha256.New, signatureKey)
	h.Write([]byte(itemCode))
	fullHash := hex.EncodeToString(h.Sum(nil))
	// Return only first 8 characters for shorter URLs
	return fullHash[:8]
}

func generateItemURL(itemCode string) (string, error) {
	baseURL, err := getBaseURL()
	if err != nil {
		return "", err
	}
	if baseURL == "" {
		return "", fmt.Errorf("base URL not configured")
	}

	itemPathPrefix, _ := getItemPathPrefix()
	signature := generateSignature(itemCode)

	var url string
	if itemPathPrefix != "" {
		url = fmt.Sprintf("%s/%s/%s?sig=%s", baseURL, itemPathPrefix, itemCode, signature)
	} else {
		url = fmt.Sprintf("%s/%s?sig=%s", baseURL, itemCode, signature)
	}
	return url, nil
}

// QROptions defines options for QR code generation
type QROptions struct {
	ErrorCorrection string // "low", "medium", "high", "highest"
	OverlayText     string // Text to overlay in the center
	Size            int    // Size in pixels (default 256)
	FontSize        int    // Font size in pixels (default 0 = auto-calculate)
	Border          int    // Border width in pixels (default 16)
}

func generateQRCode(itemCode string, opts QROptions) ([]byte, error) {
	url, err := generateItemURL(itemCode)
	if err != nil {
		return nil, err
	}

	// Set defaults
	if opts.Size == 0 {
		opts.Size = 256
	}
	if opts.ErrorCorrection == "" {
		opts.ErrorCorrection = "medium"
	}
	if opts.Border == 0 {
		opts.Border = 16 // Default 16 pixels
	}

	// Map error correction level using option functions
	var ecOpt qrcode.EncodeOption
	switch strings.ToLower(opts.ErrorCorrection) {
	case "low":
		ecOpt = qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionLow)
	case "medium":
		ecOpt = qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium)
	case "high", "q":
		ecOpt = qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionQuart)
	case "highest", "h":
		ecOpt = qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionHighest)
	default:
		ecOpt = qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium)
	}

	// Create QR code with reduced quiet zone (border)
	qrc, err := qrcode.NewWith(url,
		qrcode.WithEncodingMode(qrcode.EncModeAuto),
		ecOpt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create QR code: %w", err)
	}

	// Create temporary file for QR code generation
	tmpFile, err := ioutil.TempFile("", "qr-*.png")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Get the actual QR code dimensions (number of modules)
	qrModuleCount := qrc.Dimension()

	// Calculate module width for the QR content
	// opts.Size is the desired QR content size, border will be added on top
	qrContentSize := opts.Size
	if qrContentSize < 10 {
		qrContentSize = 10 // Minimum size for scannability
	}

	moduleWidth := uint8(qrContentSize / qrModuleCount)
	if moduleWidth < 1 {
		moduleWidth = 1 // Minimum 1 pixel per module
	}

	// Generate QR code with 0 border - we'll add our own border
	w, err := standard.New(tmpPath,
		standard.WithQRWidth(moduleWidth),
		standard.WithBorderWidth(0), // No border from library
		standard.WithBuiltinImageEncoder(standard.PNG_FORMAT),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create writer: %w", err)
	}

	if err = qrc.Save(w); err != nil {
		return nil, fmt.Errorf("failed to save QR code: %w", err)
	}

	// Calculate total image size (content + border on all sides)
	totalSize := opts.Size + (2 * opts.Border)
	log.Printf("Generated QR code for item %s, URL: %s, Content: %dpx, Border: %dpx, Total: %dpx, EC: %s",
		itemCode, url, opts.Size, opts.Border, totalSize, opts.ErrorCorrection)

	// Read the generated PNG file
	qrBytes, err := ioutil.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read QR file: %w", err)
	}

	// Decode the QR code image
	qrImg, err := png.Decode(bytes.NewReader(qrBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode QR PNG: %w", err)
	}

	// Add white border around the QR code
	qrImg = addWhiteBorder(qrImg, opts.Border)

	// If no overlay text, encode and return
	if opts.OverlayText == "" {
		var buf bytes.Buffer
		if err := encodePNGWith300DPI(&buf, qrImg); err != nil {
			return nil, fmt.Errorf("failed to encode bordered QR: %w", err)
		}
		return buf.Bytes(), nil
	}

	// Add text overlay to the bordered image
	overlayImg, err := addTextOverlay(qrImg, opts.OverlayText, opts.FontSize)
	if err != nil {
		return nil, fmt.Errorf("failed to add text overlay: %w", err)
	}

	// Encode final image
	var buf bytes.Buffer
	if err := encodePNGWith300DPI(&buf, overlayImg); err != nil {
		return nil, fmt.Errorf("failed to encode final QR: %w", err)
	}
	return buf.Bytes(), nil
}

// encodePNGWith300DPI encodes an image as PNG with 300 DPI metadata
func encodePNGWith300DPI(w io.Writer, img image.Image) error {
	encoder := png.Encoder{
		CompressionLevel: png.DefaultCompression,
	}

	// Create a temporary buffer to encode the image
	var buf bytes.Buffer
	if err := encoder.Encode(&buf, img); err != nil {
		return err
	}

	// Read the PNG and modify the pHYs chunk to set 300 DPI
	// 300 DPI = 11811 pixels per meter (300 / 0.0254)
	pngData := buf.Bytes()

	// Find the IDAT chunk position
	idatPos := bytes.Index(pngData, []byte("IDAT"))
	if idatPos == -1 {
		// If we can't find IDAT, just write the original
		_, err := w.Write(pngData)
		return err
	}

	// Create pHYs chunk for 300 DPI
	// pHYs chunk format: 4 bytes length, 4 bytes "pHYs", 4 bytes X pixels per unit,
	// 4 bytes Y pixels per unit, 1 byte unit (1 = meter), 4 bytes CRC
	pixelsPerMeter := uint32(11811) // 300 DPI in pixels per meter
	physChunk := []byte{
		0x00, 0x00, 0x00, 0x09, // Length: 9 bytes
		0x70, 0x48, 0x59, 0x73, // "pHYs"
		byte(pixelsPerMeter >> 24), byte(pixelsPerMeter >> 16), byte(pixelsPerMeter >> 8), byte(pixelsPerMeter), // X pixels per meter
		byte(pixelsPerMeter >> 24), byte(pixelsPerMeter >> 16), byte(pixelsPerMeter >> 8), byte(pixelsPerMeter), // Y pixels per meter
		0x01, // Unit: meter
	}

	// Calculate CRC for pHYs chunk (chunk type + data)
	crc := crc32.NewIEEE()
	crc.Write(physChunk[4:]) // "pHYs" + data
	crcValue := crc.Sum32()
	physChunk = append(physChunk, byte(crcValue>>24), byte(crcValue>>16), byte(crcValue>>8), byte(crcValue))

	// Write PNG header + pHYs chunk + rest of PNG
	w.Write(pngData[:idatPos-4]) // Everything before IDAT chunk
	w.Write(physChunk)           // pHYs chunk
	w.Write(pngData[idatPos-4:]) // IDAT and rest

	return nil
}

// addWhiteBorder adds a white border of specified pixel width around an image
func addWhiteBorder(img image.Image, borderWidth int) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create new image with border
	newWidth := width + 2*borderWidth
	newHeight := height + 2*borderWidth
	bordered := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Fill with white
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			bordered.Set(x, y, white)
		}
	}

	// Draw original image in the center
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			bordered.Set(x+borderWidth, y+borderWidth, img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}

	return bordered
}

// addTextOverlay adds text in the center of the QR code with a white background
func addTextOverlay(img image.Image, text string, customFontSize int) (image.Image, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create a new RGBA image
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	// Create drawing context
	dc := gg.NewContextForRGBA(rgba)

	// Disable anti-aliasing for pure black and white output (no greyscale)
	dc.SetRGBA(0, 0, 0, 0) // This will be overridden, but sets up the context

	// Calculate font size based on image size and text length
	var fontSize float64
	if customFontSize > 0 {
		fontSize = float64(customFontSize)
	} else {
		// Auto-calculate font size
		fontSize = float64(width) / 12.0
		if len(text) > 3 {
			fontSize = float64(width) / float64(len(text)*3)
		}
	}

	// Try to load font with multiple fallback options
	fontLoaded := false
	fontPaths := []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
		"/usr/share/fonts/TTF/DejaVuSans-Bold.ttf",
		"/System/Library/Fonts/Helvetica.ttc", // macOS
		"C:\\Windows\\Fonts\\arial.ttf",       // Windows
	}

	for _, fontPath := range fontPaths {
		if err := dc.LoadFontFace(fontPath, fontSize); err == nil {
			fontLoaded = true
			break
		}
	}

	// If no system fonts available, use embedded Go font as fallback
	if !fontLoaded {
		font, err := truetype.Parse(goregular.TTF)
		if err != nil {
			log.Printf("Warning: Could not parse embedded font: %v", err)
			return rgba, nil
		}
		face := truetype.NewFace(font, &truetype.Options{
			Size: fontSize,
		})
		dc.SetFontFace(face)
	}

	textWidth, _ := dc.MeasureString(text)

	// Calculate center position
	x := float64(width) / 2.0
	y := float64(height) / 2.0

	// Draw white background rectangle with equal padding on all sides
	padding := fontSize * 0.4
	rectWidth := textWidth + padding*2
	rectHeight := fontSize + padding*2 // Use fontSize height for consistent padding

	dc.SetColor(color.White)
	dc.DrawRectangle(x-rectWidth/2, y-rectHeight/2, rectWidth, rectHeight)
	dc.Fill()

	// Draw black text centered in the rectangle
	dc.SetColor(color.Black)
	dc.DrawStringAnchored(text, x, y, 0.5, 0.5)

	// Convert to pure black and white (remove anti-aliasing greyscale)
	finalImg := image.NewRGBA(dc.Image().Bounds())
	draw.Draw(finalImg, finalImg.Bounds(), dc.Image(), image.Point{}, draw.Src)

	// Threshold the image to remove greyscale pixels
	bounds2 := finalImg.Bounds()
	for y := bounds2.Min.Y; y < bounds2.Max.Y; y++ {
		for x := bounds2.Min.X; x < bounds2.Max.X; x++ {
			r, g, b, a := finalImg.At(x, y).RGBA()
			// Convert to greyscale and threshold at 50%
			grey := (r + g + b) / 3
			if grey > 32768 { // 50% threshold (65535/2)
				finalImg.Set(x, y, color.White)
			} else {
				finalImg.Set(x, y, color.Black)
			}
			_ = a // Keep alpha component unused
		}
	}

	return finalImg, nil
}

func generateQRCodeBase64(itemCode string, opts QROptions) (string, error) {
	qrBytes, err := generateQRCode(itemCode, opts)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(qrBytes), nil
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Check if setup is completed
	setupCompleted, err := isSetupCompleted()
	if err != nil {
		log.Printf("Error checking setup status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !setupCompleted {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	// Get search parameter
	searchQuery := r.URL.Query().Get("search")

	// Get sorting parameters
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "item_code" // default
	}
	sortOrder := r.URL.Query().Get("order")
	if sortOrder == "" {
		if sortBy == "item_code" {
			sortOrder = "asc" // default for item_code
		} else {
			sortOrder = "desc" // default for other columns
		}
	}

	var items []Item

	if searchQuery != "" {
		// Search for items
		items, err = searchItems(searchQuery)
		if err != nil {
			log.Printf("Error searching items: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// If exactly one result, redirect to edit page
		if len(items) == 1 {
			http.Redirect(w, r, fmt.Sprintf("/items/edit?id=%d", items[0].ID), http.StatusSeeOther)
			return
		}
	} else {
		items, err = getAllItemsSorted(sortBy, sortOrder)
		if err != nil {
			log.Printf("Error fetching items: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	owners, err := getAllOwners()
	if err != nil {
		log.Printf("Error fetching owners: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	prefixes, err := getAllPrefixes()
	if err != nil {
		log.Printf("Error fetching prefixes: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Generate signatures for all items
	itemsWithSig := make([]map[string]interface{}, len(items))
	for i, item := range items {
		sig := generateSignature(item.ItemCode)
		itemsWithSig[i] = map[string]interface{}{
			"Item":      item,
			"Signature": sig,
		}
	}

	baseURL, _ := getBaseURL()
	itemPathPrefix, _ := getItemPathPrefix()

	data := map[string]interface{}{
		"Items":          itemsWithSig,
		"Owners":         owners,
		"Prefixes":       prefixes,
		"BaseURL":        baseURL,
		"ItemPathPrefix": itemPathPrefix,
		"SortBy":         sortBy,
		"SortOrder":      sortOrder,
		"SearchQuery":    searchQuery,
		"Version":        Version,
		"GitCommit":      GitCommit,
		"BuildTime":      BuildTime,
	}

	if err := templates.ExecuteTemplate(w, "admin.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleOwners(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Show create form using edit-owner.html template
		owners, _ := getAllOwners()
		baseURL, _ := getBaseURL()
		data := map[string]interface{}{
			"Owner":   nil, // nil means creating new
			"Owners":  owners,
			"BaseURL": baseURL,
		}

		if err := templates.ExecuteTemplate(w, "edit-owner.html", data); err != nil {
			log.Printf("Error rendering template: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Join multiple checkbox values into comma-separated string
		displayOptions := r.Form["phone_display_options"]
		phoneDisplayOptions := ""
		if len(displayOptions) > 0 {
			phoneDisplayOptions = strings.Join(displayOptions, ",")
		}

		owner := Owner{
			Name:                r.FormValue("name"),
			Initials:            r.FormValue("initials"),
			Email:               r.FormValue("email"),
			Phone:               r.FormValue("phone"),
			PhoneDisplayOptions: phoneDisplayOptions,
		}

		if _, err := createOwner(&owner); err != nil {
			log.Printf("Error creating owner: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Created owner: %s (%s)", owner.Name, owner.Email)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleSetup(w http.ResponseWriter, r *http.Request) {
	// Check if setup is already completed
	setupCompleted, err := isSetupCompleted()
	if err != nil {
		log.Printf("Error checking setup status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Method == "POST" {
		// Mark setup as completed
		if err := setSetupCompleted(); err != nil {
			log.Printf("Error marking setup as completed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("Setup completed")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// GET request - show setup page
	owners, _ := getAllOwners()
	prefixes, _ := getAllPrefixes()
	baseURL, _ := getBaseURL()

	data := map[string]interface{}{
		"Owners":       owners,
		"Prefixes":     prefixes,
		"BaseURL":      baseURL,
		"SignatureKey": getSignatureKeyHex(),
		"SetupMode":    !setupCompleted,
	}

	if err := templates.ExecuteTemplate(w, "setup.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	// Ensure setup is completed before accessing settings
	setupCompleted, err := isSetupCompleted()
	if err != nil {
		log.Printf("Error checking setup status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !setupCompleted {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	owners, _ := getAllOwners()
	prefixes, _ := getAllPrefixes()
	baseURL, _ := getBaseURL()
	itemPathPrefix, _ := getItemPathPrefix()
	qrSize, _ := getQRSize()
	qrErrorCorrection, _ := getQRErrorCorrection()
	qrOverlay, _ := getQROverlay()
	qrFontSize, _ := getQRFontSize()
	qrBorder, _ := getQRBorder()

	data := map[string]interface{}{
		"Owners":            owners,
		"Prefixes":          prefixes,
		"BaseURL":           baseURL,
		"ItemPathPrefix":    itemPathPrefix,
		"QRSize":            qrSize,
		"QRErrorCorrection": qrErrorCorrection,
		"QROverlay":         qrOverlay,
		"QRFontSize":        qrFontSize,
		"QRBorder":          qrBorder,
		"SignatureKey":      getSignatureKeyHex(),
		"Version":           Version,
		"GitCommit":         GitCommit,
		"BuildTime":         BuildTime,
	}

	if err := templates.ExecuteTemplate(w, "settings.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleEditOwner(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Show edit form
		ownerIDStr := r.URL.Query().Get("id")
		if ownerIDStr == "" {
			http.Error(w, "Owner ID required", http.StatusBadRequest)
			return
		}

		var ownerID int
		fmt.Sscanf(ownerIDStr, "%d", &ownerID)

		owner, err := getOwnerByID(ownerID)
		if err != nil {
			log.Printf("Error fetching owner %d: %v", ownerID, err)
			http.Error(w, "Owner not found", http.StatusNotFound)
			return
		}

		owners, _ := getAllOwners()
		baseURL, _ := getBaseURL()
		data := map[string]interface{}{
			"Owner":   owner,
			"Owners":  owners,
			"BaseURL": baseURL,
		}

		if err := templates.ExecuteTemplate(w, "edit-owner.html", data); err != nil {
			log.Printf("Error rendering template: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method == "POST" {
		// Update owner
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var ownerID int
		fmt.Sscanf(r.FormValue("id"), "%d", &ownerID)

		displayOptions := r.Form["phone_display_options"]
		phoneDisplayOptions := ""
		if len(displayOptions) > 0 {
			phoneDisplayOptions = strings.Join(displayOptions, ",")
		}

		owner := Owner{
			ID:                  ownerID,
			Name:                r.FormValue("name"),
			Initials:            r.FormValue("initials"),
			Email:               r.FormValue("email"),
			Phone:               r.FormValue("phone"),
			PhoneDisplayOptions: phoneDisplayOptions,
		}

		if err := updateOwner(&owner); err != nil {
			log.Printf("Error updating owner: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Updated owner: %s (%s)", owner.Name, owner.Email)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleNewItem(w http.ResponseWriter, r *http.Request) {
	// Check if setup is completed
	setupCompleted, err := isSetupCompleted()
	if err != nil {
		log.Printf("Error checking setup status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !setupCompleted {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	owners, err := getAllOwners()
	if err != nil {
		log.Printf("Error fetching owners: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	prefixes, err := getAllPrefixes()
	if err != nil {
		log.Printf("Error fetching prefixes: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	baseURL, _ := getBaseURL()
	itemPathPrefix, _ := getItemPathPrefix()

	data := map[string]interface{}{
		"Owners":         owners,
		"Prefixes":       prefixes,
		"BaseURL":        baseURL,
		"ItemPathPrefix": itemPathPrefix,
	}

	if err := templates.ExecuteTemplate(w, "add-item.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleItems(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		creationMode := r.FormValue("creation_mode")
		ownerIDs := r.Form["owner_ids"]

		// Get owner IDs as integers
		var ownerIDsInt []int
		for _, ownerIDStr := range ownerIDs {
			var ownerID int
			fmt.Sscanf(ownerIDStr, "%d", &ownerID)
			ownerIDsInt = append(ownerIDsInt, ownerID)
		}

		if creationMode == "batch" {
			// Batch creation mode
			var prefixID int
			fmt.Sscanf(r.FormValue("prefix_id"), "%d", &prefixID)

			rangeStart := r.FormValue("range_start")
			rangeEnd := r.FormValue("range_end")
			description := r.FormValue("description")

			var start, end int
			if _, err := fmt.Sscanf(rangeStart, "%d", &start); err != nil {
				http.Error(w, "Invalid start range", http.StatusBadRequest)
				return
			}
			if _, err := fmt.Sscanf(rangeEnd, "%d", &end); err != nil {
				http.Error(w, "Invalid end range", http.StatusBadRequest)
				return
			}

			if start > end {
				http.Error(w, "Start range must be less than or equal to end range", http.StatusBadRequest)
				return
			}

			if end-start > 1000 {
				http.Error(w, "Range too large (max 1000 items)", http.StatusBadRequest)
				return
			}

			// Create items in batch
			createdCount := 0
			for i := start; i <= end; i++ {
				itemCode, err := generateItemCodeFromPrefix(prefixID)
				if err != nil {
					log.Printf("Error generating item code: %v", err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				item := Item{
					ItemCode:    itemCode,
					Description: description,
				}

				itemID, err := createItem(&item)
				if err != nil {
					log.Printf("Error creating item: %v", err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				// Link owners to item
				for _, ownerID := range ownerIDsInt {
					if err := linkItemToOwner(itemID, ownerID); err != nil {
						log.Printf("Error linking item to owner: %v", err)
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				createdCount++
			}

			log.Printf("Created %d items in batch with %d owner(s)", createdCount, len(ownerIDsInt))
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return

		} else if creationMode == "single" {
			// Single item creation mode
			var itemCode string
			usePrefix := r.FormValue("use_prefix")

			if usePrefix == "yes" {
				// Auto-generate item code from prefix
				var prefixID int
				fmt.Sscanf(r.FormValue("prefix_id_single"), "%d", &prefixID)

				generatedCode, err := generateItemCodeFromPrefix(prefixID)
				if err != nil {
					log.Printf("Error generating item code: %v", err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				itemCode = generatedCode
			} else {
				// Use manually entered item code
				itemCode = r.FormValue("item_code")
			}

			item := Item{
				ItemCode:    itemCode,
				Description: r.FormValue("description"),
			}

			itemID, err := createItem(&item)
			if err != nil {
				log.Printf("Error creating item: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Link owners to item
			for _, ownerID := range ownerIDsInt {
				if err := linkItemToOwner(itemID, ownerID); err != nil {
					log.Printf("Error linking item to owner: %v", err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}

			log.Printf("Created item: %s with %d owner(s)", item.ItemCode, len(ownerIDsInt))
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		http.Error(w, "Invalid creation mode", http.StatusBadRequest)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handlePrefixes(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		prefix := IdentifierPrefix{
			Prefix:         r.FormValue("prefix"),
			CurrentCounter: 1,
		}

		if err := createPrefix(&prefix); err != nil {
			log.Printf("Error creating prefix: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Created prefix: %s", prefix.Prefix)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	baseURL := r.FormValue("base_url")
	itemPathPrefix := r.FormValue("item_path_prefix")

	if err := setBaseURL(baseURL); err != nil {
		log.Printf("Error setting base URL: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := setItemPathPrefix(itemPathPrefix); err != nil {
		log.Printf("Error setting item path prefix: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Updated base URL: %s", baseURL)
	log.Printf("Updated item path prefix: %s", itemPathPrefix)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func handleQRConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse QR size
	qrSizeStr := r.FormValue("qr_size")
	var qrSize int
	fmt.Sscanf(qrSizeStr, "%d", &qrSize)
	if qrSize < 32 {
		qrSize = 32
	}
	if qrSize > 1024 {
		qrSize = 1024
	}

	errorCorrection := r.FormValue("qr_error_correction")
	overlay := r.FormValue("qr_overlay")

	// Parse font size
	fontSizeStr := r.FormValue("qr_font_size")
	var fontSize int
	fmt.Sscanf(fontSizeStr, "%d", &fontSize)
	if fontSize < 0 {
		fontSize = 0
	}
	if fontSize > 200 {
		fontSize = 200
	}

	// Validate error correction
	validEC := map[string]bool{"low": true, "medium": true, "high": true, "highest": true}
	if !validEC[errorCorrection] {
		errorCorrection = "medium"
	}

	// Validate overlay
	validOverlay := map[string]bool{"none": true, "initials": true, "lastname": true}
	if !validOverlay[overlay] {
		overlay = "none"
	}

	if err := setQRSize(qrSize); err != nil {
		log.Printf("Error setting QR size: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := setQRErrorCorrection(errorCorrection); err != nil {
		log.Printf("Error setting QR error correction: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := setQROverlay(overlay); err != nil {
		log.Printf("Error setting QR overlay: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := setQRFontSize(fontSize); err != nil {
		log.Printf("Error setting QR font size: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Parse border width (in pixels)
	borderStr := r.FormValue("qr_border")
	var border int
	fmt.Sscanf(borderStr, "%d", &border)
	if border < 1 {
		border = 1
	}
	if border > 100 {
		border = 100
	}

	if err := setQRBorder(border); err != nil {
		log.Printf("Error setting QR border: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Updated QR settings - Size: %d, Error Correction: %s, Overlay: %s, Font Size: %d, Border: %d", qrSize, errorCorrection, overlay, fontSize, border)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func handleEditItem(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Show edit form
		itemIDStr := r.URL.Query().Get("id")
		if itemIDStr == "" {
			http.Error(w, "Item ID required", http.StatusBadRequest)
			return
		}

		var itemID int
		fmt.Sscanf(itemIDStr, "%d", &itemID)

		item, err := getItemByID(itemID)
		if err != nil {
			log.Printf("Error fetching item %d: %v", itemID, err)
			http.Error(w, "Item not found", http.StatusNotFound)
			return
		}

		owners, _ := getAllOwners()
		baseURL, _ := getBaseURL()
		data := map[string]interface{}{
			"Item":    item,
			"Owners":  owners,
			"BaseURL": baseURL,
		}

		if err := templates.ExecuteTemplate(w, "edit-item.html", data); err != nil {
			log.Printf("Error rendering template: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method == "POST" {
		// Update item
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var itemID int
		fmt.Sscanf(r.FormValue("id"), "%d", &itemID)

		item := Item{
			ID:          itemID,
			ItemCode:    r.FormValue("item_code"),
			Description: r.FormValue("description"),
		}

		if err := updateItem(&item); err != nil {
			log.Printf("Error updating item: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Update item-owner relationships
		// First, remove all existing relationships
		if err := unlinkAllOwnersFromItem(itemID); err != nil {
			log.Printf("Error unlinking owners from item: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Then add the new relationships
		ownerIDs := r.Form["owner_ids"]
		for _, ownerIDStr := range ownerIDs {
			var ownerID int
			fmt.Sscanf(ownerIDStr, "%d", &ownerID)
			if err := linkItemToOwner(itemID, ownerID); err != nil {
				log.Printf("Error linking item to owner: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		log.Printf("Updated item: %s with %d owner(s)", item.ItemCode, len(ownerIDs))
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleAPIPrefixes(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var prefixID int
		fmt.Sscanf(r.FormValue("id"), "%d", &prefixID)

		if err := deletePrefix(prefixID); err != nil {
			log.Printf("Error deleting prefix %d: %v", prefixID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Deleted prefix ID: %d", prefixID)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleAPIOwners(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var ownerID int
		fmt.Sscanf(r.FormValue("id"), "%d", &ownerID)

		if err := deleteOwner(ownerID); err != nil {
			log.Printf("Error deleting owner %d: %v", ownerID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Deleted owner ID: %d", ownerID)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleAPIItems(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var itemID int
		fmt.Sscanf(r.FormValue("id"), "%d", &itemID)

		if err := deleteItem(itemID); err != nil {
			log.Printf("Error deleting item %d: %v", itemID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Deleted item ID: %d", itemID)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Database unavailable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Database functions
func getAllItems() ([]Item, error) {
	return getAllItemsSorted("item_code", "asc")
}

func getAllItemsSorted(sortBy, sortOrder string) ([]Item, error) {
	// Validate sort column
	var orderClause string
	switch sortBy {
	case "item_code":
		orderClause = "item_code"
	case "exported":
		orderClause = "exported, exported_at"
	case "created":
		orderClause = "created_at"
	case "owners":
		// For owners, we'll sort in-memory after fetching
		orderClause = "item_code"
	default:
		orderClause = "created_at"
	}

	// Validate sort order
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}

	query := fmt.Sprintf(`
		SELECT id, item_code, description, created_at, exported, COALESCE(exported_at, '')
		FROM items
		ORDER BY %s %s
	`, orderClause, strings.ToUpper(sortOrder))

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		var exported int
		if err := rows.Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt, &exported, &item.ExportedAt); err != nil {
			return nil, err
		}
		item.Exported = exported == 1

		// Get owners for this item
		owners, err := getOwnersForItem(item.ID)
		if err != nil {
			return nil, err
		}
		item.Owners = owners

		items = append(items, item)
	}

	// If sorting by owners, sort in-memory
	if sortBy == "owners" {
		sortItemsByOwners(items, sortOrder)
	}

	return items, nil
}

func sortItemsByOwners(items []Item, sortOrder string) {
	// Sort items by first owner's initials/name
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			swap := false

			// Get first owner for comparison
			owner1 := ""
			owner2 := ""

			if len(items[i].Owners) > 0 {
				if items[i].Owners[0].Initials != "" {
					owner1 = items[i].Owners[0].Initials
				} else {
					owner1 = items[i].Owners[0].Name
				}
			}

			if len(items[j].Owners) > 0 {
				if items[j].Owners[0].Initials != "" {
					owner2 = items[j].Owners[0].Initials
				} else {
					owner2 = items[j].Owners[0].Name
				}
			}

			if sortOrder == "asc" {
				swap = owner1 > owner2
			} else {
				swap = owner1 < owner2
			}

			if swap {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func searchItems(query string) ([]Item, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return getAllItems()
	}

	// Check if query is a URL (starts with http:// or https://)
	isURL := strings.HasPrefix(query, "http://") || strings.HasPrefix(query, "https://")

	// Get all items
	allItems, err := getAllItems()
	if err != nil {
		return nil, err
	}

	var matchedItems []Item

	if isURL {
		// For URL search: generate URL for each item and compare as simple string
		for _, item := range allItems {
			itemURL, err := generateItemURL(item.ItemCode)
			if err != nil {
				continue // Skip items that can't generate URL
			}

			// Simple string comparison
			if itemURL == query {
				matchedItems = append(matchedItems, item)
			}
		}
	} else {
		// For item code search: partial match on item code
		queryLower := strings.ToLower(query)
		for _, item := range allItems {
			itemCodeLower := strings.ToLower(item.ItemCode)
			if strings.Contains(itemCodeLower, queryLower) {
				matchedItems = append(matchedItems, item)
			}
		}
	}

	return matchedItems, nil
}

func getUnexportedItems() ([]Item, error) {
	rows, err := db.Query(`
		SELECT id, item_code, description, created_at, exported, COALESCE(exported_at, '')
		FROM items
		WHERE exported = 0
		ORDER BY item_code ASC
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		var exported int
		if err := rows.Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt, &exported, &item.ExportedAt); err != nil {
			return nil, err
		}
		item.Exported = exported == 1

		// Get owners for this item
		owners, err := getOwnersForItem(item.ID)
		if err != nil {
			return nil, err
		}
		item.Owners = owners

		items = append(items, item)
	}

	return items, nil
}

func getAllOwners() ([]Owner, error) {
	rows, err := db.Query(`
		SELECT id, name, COALESCE(initials, ''), email, phone, phone_display_options, created_at
		FROM owners
		ORDER BY name
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var owners []Owner
	for rows.Next() {
		var owner Owner
		if err := rows.Scan(&owner.ID, &owner.Name, &owner.Initials, &owner.Email, &owner.Phone, &owner.PhoneDisplayOptions, &owner.CreatedAt); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}

	return owners, nil
}

func getOwnersForItem(itemID int) ([]Owner, error) {
	rows, err := db.Query(`
		SELECT o.id, o.name, COALESCE(o.initials, ''), o.email, o.phone, o.phone_display_options, o.created_at
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
		if err := rows.Scan(&owner.ID, &owner.Name, &owner.Initials, &owner.Email, &owner.Phone, &owner.PhoneDisplayOptions, &owner.CreatedAt); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}

	return owners, nil
}

func createOwner(owner *Owner) (int, error) {
	result, err := db.Exec(`
		INSERT INTO owners (name, initials, email, phone, phone_display_options)
		VALUES (?, ?, ?, ?, ?)
	`, owner.Name, owner.Initials, owner.Email, owner.Phone, owner.PhoneDisplayOptions)

	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	owner.ID = int(id)
	return int(id), nil
}

func createItem(item *Item) (int, error) {
	result, err := db.Exec(`
		INSERT INTO items (item_code, description)
		VALUES (?, ?)
	`, item.ItemCode, item.Description)

	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	return int(id), nil
}

func linkItemToOwner(itemID, ownerID int) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO item_owners (item_id, owner_id)
		VALUES (?, ?)
	`, itemID, ownerID)

	return err
}

func deleteOwner(ownerID int) error {
	_, err := db.Exec("DELETE FROM owners WHERE id = ?", ownerID)
	return err
}

func deleteItem(itemID int) error {
	_, err := db.Exec("DELETE FROM items WHERE id = ?", itemID)
	return err
}

func getOwnerByID(ownerID int) (*Owner, error) {
	owner := &Owner{}
	err := db.QueryRow(`
		SELECT id, name, COALESCE(initials, ''), email, phone, phone_display_options, created_at
		FROM owners
		WHERE id = ?
	`, ownerID).Scan(&owner.ID, &owner.Name, &owner.Initials, &owner.Email, &owner.Phone, &owner.PhoneDisplayOptions, &owner.CreatedAt)

	if err != nil {
		return nil, err
	}
	return owner, nil
}

func updateOwner(owner *Owner) error {
	_, err := db.Exec(`
		UPDATE owners
		SET name = ?, initials = ?, email = ?, phone = ?, phone_display_options = ?
		WHERE id = ?
	`, owner.Name, owner.Initials, owner.Email, owner.Phone, owner.PhoneDisplayOptions, owner.ID)
	return err
}

func getItemByID(itemID int) (*Item, error) {
	item := &Item{}
	var exported int
	err := db.QueryRow(`
		SELECT id, item_code, description, created_at, exported, COALESCE(exported_at, '')
		FROM items
		WHERE id = ?
	`, itemID).Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt, &exported, &item.ExportedAt)

	if err != nil {
		return nil, err
	}
	item.Exported = exported == 1

	// Get owners for this item
	owners, err := getOwnersForItem(item.ID)
	if err != nil {
		return nil, err
	}
	item.Owners = owners

	return item, nil
}

func getItemByCode(itemCode string) (*Item, error) {
	item := &Item{}
	var exported int
	err := db.QueryRow(`
		SELECT id, item_code, description, created_at, exported, COALESCE(exported_at, '')
		FROM items
		WHERE item_code = ?
	`, itemCode).Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt, &exported, &item.ExportedAt)

	if err != nil {
		return nil, err
	}
	item.Exported = exported == 1

	// Get owners for this item
	owners, err := getOwnersForItem(item.ID)
	if err != nil {
		return nil, err
	}
	item.Owners = owners

	return item, nil
}

func getItemsByRange(startCode, endCode string) ([]Item, error) {
	rows, err := db.Query(`
		SELECT id, item_code, description, created_at, exported, COALESCE(exported_at, '')
		FROM items
		WHERE item_code BETWEEN ? AND ?
		ORDER BY item_code ASC
	`, startCode, endCode)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		var exported int
		if err := rows.Scan(&item.ID, &item.ItemCode, &item.Description, &item.CreatedAt, &exported, &item.ExportedAt); err != nil {
			return nil, err
		}
		item.Exported = exported == 1

		// Get owners for this item
		owners, err := getOwnersForItem(item.ID)
		if err != nil {
			return nil, err
		}
		item.Owners = owners

		items = append(items, item)
	}

	return items, nil
}

func markItemsAsExported(itemIDs []int) error {
	if len(itemIDs) == 0 {
		return nil
	}

	// Build placeholders for SQL IN clause
	placeholders := make([]string, len(itemIDs))
	args := make([]interface{}, len(itemIDs))
	for i, id := range itemIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		UPDATE items
		SET exported = 1, exported_at = CURRENT_TIMESTAMP
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	_, err := db.Exec(query, args...)
	return err
}

func updateItem(item *Item) error {
	_, err := db.Exec(`
		UPDATE items
		SET item_code = ?, description = ?
		WHERE id = ?
	`, item.ItemCode, item.Description, item.ID)

	return err
}

func unlinkAllOwnersFromItem(itemID int) error {
	_, err := db.Exec("DELETE FROM item_owners WHERE item_id = ?", itemID)
	return err
}

func getAllPrefixes() ([]IdentifierPrefix, error) {
	rows, err := db.Query(`
		SELECT id, prefix, current_counter, created_at
		FROM identifier_prefixes
		ORDER BY prefix
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prefixes []IdentifierPrefix
	for rows.Next() {
		var prefix IdentifierPrefix
		if err := rows.Scan(&prefix.ID, &prefix.Prefix, &prefix.CurrentCounter, &prefix.CreatedAt); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}

	return prefixes, nil
}

func createPrefix(prefix *IdentifierPrefix) error {
	result, err := db.Exec(`
		INSERT INTO identifier_prefixes (prefix, current_counter)
		VALUES (?, ?)
	`, prefix.Prefix, prefix.CurrentCounter)

	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	prefix.ID = int(id)
	return nil
}

func deletePrefix(prefixID int) error {
	_, err := db.Exec("DELETE FROM identifier_prefixes WHERE id = ?", prefixID)
	return err
}

func generateItemCodeFromPrefix(prefixID int) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var prefix string
	var counter int
	err = tx.QueryRow(`
		SELECT prefix, current_counter
		FROM identifier_prefixes
		WHERE id = ?
	`, prefixID).Scan(&prefix, &counter)

	if err != nil {
		return "", err
	}

	itemCode := fmt.Sprintf("%s-%d", prefix, counter)

	// Increment counter
	_, err = tx.Exec(`
		UPDATE identifier_prefixes
		SET current_counter = current_counter + 1
		WHERE id = ?
	`, prefixID)

	if err != nil {
		return "", err
	}

	if err = tx.Commit(); err != nil {
		return "", err
	}

	return itemCode, nil
}

func handleQRSingle(w http.ResponseWriter, r *http.Request) {
	itemCode := strings.TrimPrefix(r.URL.Path, "/qr/single/")
	if itemCode == "" {
		http.Error(w, "Item code required", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	mark := r.URL.Query().Get("mark") == "true"
	errorCorrection := r.URL.Query().Get("errorCorrection")
	overlay := r.URL.Query().Get("overlay")

	item, err := getItemByCode(itemCode)
	if err != nil {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}

	// Get default QR settings from config
	defaultSize, _ := getQRSize()
	defaultEC, _ := getQRErrorCorrection()
	defaultOverlay, _ := getQROverlay()
	defaultFontSize, _ := getQRFontSize()
	defaultBorder, _ := getQRBorder()

	// Use query params if provided, otherwise use defaults
	if errorCorrection == "" {
		errorCorrection = defaultEC
	}
	if overlay == "" {
		overlay = defaultOverlay
	}

	// Prepare QR options
	opts := QROptions{
		ErrorCorrection: errorCorrection,
		Size:            defaultSize,
		FontSize:        defaultFontSize,
		Border:          defaultBorder,
	}

	// Handle overlay text
	if overlay != "" && len(item.Owners) > 0 {
		switch strings.ToLower(overlay) {
		case "initials":
			// Collect all owners' initials
			var initials []string
			for _, owner := range item.Owners {
				if owner.Initials != "" {
					initials = append(initials, owner.Initials)
				}
			}
			opts.OverlayText = strings.Join(initials, " ")
		case "lastname":
			// Collect all owners' last names
			var lastNames []string
			for _, owner := range item.Owners {
				nameParts := strings.Fields(owner.Name)
				if len(nameParts) > 0 {
					lastNames = append(lastNames, nameParts[len(nameParts)-1])
				}
			}
			opts.OverlayText = strings.Join(lastNames, " ")
		}
	}

	if format == "png" {
		// Download as PNG
		qrBytes, err := generateQRCode(itemCode, opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if mark {
			markItemsAsExported([]int{item.ID})
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.png", itemCode))
		w.Write(qrBytes)
		return
	}

	// Default: Show printable page
	qrBase64, err := generateQRCodeBase64(itemCode, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	url, _ := generateItemURL(itemCode)

	data := map[string]interface{}{
		"ItemCode": itemCode,
		"Item":     item,
		"QRCode":   qrBase64,
		"URL":      url,
	}

	if mark {
		markItemsAsExported([]int{item.ID})
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<title>QR Code - {{.ItemCode}}</title>
	<style>
		@media print {
			.no-print { display: none; }
			@page { margin: 0.5in; }
		}
		body {
			font-family: Arial, sans-serif;
			max-width: 800px;
			margin: 0 auto;
			padding: 20px;
		}
		.qr-container {
			text-align: center;
			page-break-inside: avoid;
			margin: 20px 0;
			padding: 20px;
			border: 2px dashed #ccc;
		}
		.qr-code {
			margin: 20px auto;
		}
		.item-code {
			font-size: 24px;
			font-weight: bold;
			margin: 10px 0;
		}
		.description {
			color: #666;
			margin: 10px 0;
		}
		.url {
			font-size: 12px;
			color: #999;
			word-break: break-all;
			margin: 10px 0;
		}
		.button-bar {
			margin: 20px 0;
			text-align: center;
		}
		button {
			padding: 10px 20px;
			margin: 5px;
			cursor: pointer;
			background: #c30000;
			color: white;
			border: none;
			border-radius: 4px;
		}
		button:hover {
			background: #a00000;
		}
	</style>
</head>
<body>
	<div class="button-bar no-print">
		<button onclick="window.print()">🖨️ Print / Save as PDF</button>
		<button onclick="window.close()">Close</button>
	</div>

	<div class="qr-container">
		<div class="item-code">{{.ItemCode}}</div>
		{{if .Item.Description}}
		<div class="description">{{.Item.Description}}</div>
		{{end}}
		<div class="qr-code">
			<img src="data:image/png;base64,{{.QRCode}}" alt="QR Code" width="256" height="256">
		</div>
		<div class="url">{{.URL}}</div>
	</div>
</body>
</html>`

	t := template.Must(template.New("qr").Parse(tmpl))
	t.Execute(w, data)
}

func handleQRBatch(w http.ResponseWriter, r *http.Request) {
	startCode := r.URL.Query().Get("start")
	endCode := r.URL.Query().Get("end")
	format := r.URL.Query().Get("format")
	mark := r.URL.Query().Get("mark") == "true"
	errorCorrection := r.URL.Query().Get("errorCorrection")
	overlay := r.URL.Query().Get("overlay")

	if startCode == "" || endCode == "" {
		http.Error(w, "Start and end codes required", http.StatusBadRequest)
		return
	}

	items, err := getItemsByRange(startCode, endCode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(items) == 0 {
		http.Error(w, "No items found in range", http.StatusNotFound)
		return
	}

	// Get default QR settings from config
	defaultSize, _ := getQRSize()
	defaultEC, _ := getQRErrorCorrection()
	defaultOverlay, _ := getQROverlay()
	defaultFontSize, _ := getQRFontSize()
	defaultBorder, _ := getQRBorder()

	// Use query params if provided, otherwise use defaults
	if errorCorrection == "" {
		errorCorrection = defaultEC
	}
	if overlay == "" {
		overlay = defaultOverlay
	}

	if format == "zip" {
		// Create ZIP archive
		buf := new(bytes.Buffer)
		zipWriter := zip.NewWriter(buf)

		itemIDs := []int{}
		for _, item := range items {
			// Prepare QR options for this item
			opts := QROptions{
				ErrorCorrection: errorCorrection,
				Size:            defaultSize,
				FontSize:        defaultFontSize,
				Border:          defaultBorder,
			}

			// Handle overlay text
			if overlay != "" && len(item.Owners) > 0 {
				switch strings.ToLower(overlay) {
				case "initials":
					// Collect all owners' initials
					var initials []string
					for _, owner := range item.Owners {
						if owner.Initials != "" {
							initials = append(initials, owner.Initials)
						}
					}
					opts.OverlayText = strings.Join(initials, " ")
				case "lastname":
					// Collect all owners' last names
					var lastNames []string
					for _, owner := range item.Owners {
						nameParts := strings.Fields(owner.Name)
						if len(nameParts) > 0 {
							lastNames = append(lastNames, nameParts[len(nameParts)-1])
						}
					}
					opts.OverlayText = strings.Join(lastNames, " ")
				}
			}

			qrBytes, err := generateQRCode(item.ItemCode, opts)
			if err != nil {
				log.Printf("Error generating QR for %s: %v", item.ItemCode, err)
				continue
			}

			fileName := fmt.Sprintf("%s.png", item.ItemCode)
			f, err := zipWriter.Create(fileName)
			if err != nil {
				log.Printf("Error creating zip entry for %s: %v", item.ItemCode, err)
				continue
			}

			_, err = f.Write(qrBytes)
			if err != nil {
				log.Printf("Error writing QR to zip for %s: %v", item.ItemCode, err)
				continue
			}

			itemIDs = append(itemIDs, item.ID)
		}

		zipWriter.Close()

		if mark {
			markItemsAsExported(itemIDs)
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=qr-codes-%s-to-%s.zip", startCode, endCode))
		w.Write(buf.Bytes())
		return
	}

	// Default: Show printable page with all QR codes
	type ItemWithQR struct {
		Item
		QRCode string
		URL    string
	}

	itemsWithQR := []ItemWithQR{}
	itemIDs := []int{}
	for _, item := range items {
		// Prepare QR options for this item
		opts := QROptions{
			ErrorCorrection: errorCorrection,
			Size:            defaultSize,
			FontSize:        defaultFontSize,
			Border:          defaultBorder,
		}

		// Handle overlay text
		if overlay != "" && len(item.Owners) > 0 {
			switch strings.ToLower(overlay) {
			case "initials":
				// Collect all owners' initials
				var initials []string
				for _, owner := range item.Owners {
					if owner.Initials != "" {
						initials = append(initials, owner.Initials)
					}
				}
				opts.OverlayText = strings.Join(initials, " ")
			case "lastname":
				// Collect all owners' last names
				var lastNames []string
				for _, owner := range item.Owners {
					nameParts := strings.Fields(owner.Name)
					if len(nameParts) > 0 {
						lastNames = append(lastNames, nameParts[len(nameParts)-1])
					}
				}
				opts.OverlayText = strings.Join(lastNames, " ")
			}
		}

		qrBase64, err := generateQRCodeBase64(item.ItemCode, opts)
		if err != nil {
			log.Printf("Error generating QR for %s: %v", item.ItemCode, err)
			continue
		}

		url, _ := generateItemURL(item.ItemCode)

		itemsWithQR = append(itemsWithQR, ItemWithQR{
			Item:   item,
			QRCode: qrBase64,
			URL:    url,
		})
		itemIDs = append(itemIDs, item.ID)
	}

	if mark {
		markItemsAsExported(itemIDs)
	}

	data := map[string]interface{}{
		"Items":     itemsWithQR,
		"StartCode": startCode,
		"EndCode":   endCode,
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<title>QR Codes - {{.StartCode}} to {{.EndCode}}</title>
	<style>
		@media print {
			.no-print { display: none; }
			@page { margin: 0.5in; }
		}
		body {
			font-family: Arial, sans-serif;
			margin: 0;
			padding: 20px;
		}
		.button-bar {
			margin: 20px 0;
			text-align: center;
		}
		button {
			padding: 10px 20px;
			margin: 5px;
			cursor: pointer;
			background: #c30000;
			color: white;
			border: none;
			border-radius: 4px;
		}
		button:hover {
			background: #a00000;
		}
		.qr-grid {
			display: grid;
			grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
			gap: 20px;
			margin: 20px 0;
		}
		.qr-container {
			text-align: center;
			page-break-inside: avoid;
			padding: 15px;
			border: 2px dashed #ccc;
		}
		.item-code {
			font-size: 18px;
			font-weight: bold;
			margin: 10px 0;
		}
		.description {
			color: #666;
			font-size: 12px;
			margin: 5px 0;
		}
		.qr-code {
			margin: 10px auto;
		}
		.url {
			font-size: 10px;
			color: #999;
			word-break: break-all;
			margin: 5px 0;
		}
	</style>
</head>
<body>
	<div class="button-bar no-print">
		<h2>QR Codes: {{.StartCode}} to {{.EndCode}} ({{len .Items}} items)</h2>
		<button onclick="window.print()">🖨️ Print / Save as PDF</button>
		<button onclick="window.close()">Close</button>
	</div>

	<div class="qr-grid">
		{{range .Items}}
		<div class="qr-container">
			<div class="item-code">{{.ItemCode}}</div>
			{{if .Description}}
			<div class="description">{{.Description}}</div>
			{{end}}
			<div class="qr-code">
				<img src="data:image/png;base64,{{.QRCode}}" alt="QR Code" width="200" height="200">
			</div>
			<div class="url">{{.URL}}</div>
		</div>
		{{end}}
	</div>
</body>
</html>`

	t := template.Must(template.New("qr").Parse(tmpl))
	t.Execute(w, data)
}

func handleQRUnexported(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	mark := r.URL.Query().Get("mark") == "true"
	errorCorrection := r.URL.Query().Get("errorCorrection")
	overlay := r.URL.Query().Get("overlay")

	items, err := getUnexportedItems()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(items) == 0 {
		http.Error(w, "No unexported items found", http.StatusNotFound)
		return
	}

	// Get default QR settings from config
	defaultSize, _ := getQRSize()
	defaultEC, _ := getQRErrorCorrection()
	defaultOverlay, _ := getQROverlay()
	defaultFontSize, _ := getQRFontSize()
	defaultBorder, _ := getQRBorder()

	// Use query params if provided, otherwise use defaults
	if errorCorrection == "" {
		errorCorrection = defaultEC
	}
	if overlay == "" {
		overlay = defaultOverlay
	}

	if format == "zip" {
		// Create ZIP archive
		buf := new(bytes.Buffer)
		zipWriter := zip.NewWriter(buf)

		itemIDs := []int{}
		for _, item := range items {
			// Prepare QR options for this item
			opts := QROptions{
				ErrorCorrection: errorCorrection,
				Size:            defaultSize,
				FontSize:        defaultFontSize,
				Border:          defaultBorder,
			}

			// Handle overlay text
			if overlay != "" && len(item.Owners) > 0 {
				switch strings.ToLower(overlay) {
				case "initials":
					// Collect all owners' initials
					var initials []string
					for _, owner := range item.Owners {
						if owner.Initials != "" {
							initials = append(initials, owner.Initials)
						}
					}
					opts.OverlayText = strings.Join(initials, " ")
				case "lastname":
					// Collect all owners' last names
					var lastNames []string
					for _, owner := range item.Owners {
						nameParts := strings.Fields(owner.Name)
						if len(nameParts) > 0 {
							lastNames = append(lastNames, nameParts[len(nameParts)-1])
						}
					}
					opts.OverlayText = strings.Join(lastNames, " ")
				}
			}

			qrBytes, err := generateQRCode(item.ItemCode, opts)
			if err != nil {
				log.Printf("Error generating QR for %s: %v", item.ItemCode, err)
				continue
			}

			fileName := fmt.Sprintf("%s.png", item.ItemCode)
			f, err := zipWriter.Create(fileName)
			if err != nil {
				log.Printf("Error creating zip entry for %s: %v", item.ItemCode, err)
				continue
			}

			_, err = f.Write(qrBytes)
			if err != nil {
				log.Printf("Error writing QR to zip for %s: %v", item.ItemCode, err)
				continue
			}

			itemIDs = append(itemIDs, item.ID)
		}

		zipWriter.Close()

		if mark {
			markItemsAsExported(itemIDs)
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=unexported-qr-codes-%s.zip", time.Now().Format("2006-01-02")))
		w.Write(buf.Bytes())
		return
	}

	// Default: Show printable page with all QR codes
	type ItemWithQR struct {
		Item
		QRCode string
		URL    string
	}

	itemsWithQR := []ItemWithQR{}
	itemIDs := []int{}
	for _, item := range items {
		// Prepare QR options for this item
		opts := QROptions{
			ErrorCorrection: errorCorrection,
			Size:            defaultSize,
			FontSize:        defaultFontSize,
			Border:          defaultBorder,
		}

		// Handle overlay text
		if overlay != "" && len(item.Owners) > 0 {
			switch strings.ToLower(overlay) {
			case "initials":
				// Collect all owners' initials
				var initials []string
				for _, owner := range item.Owners {
					if owner.Initials != "" {
						initials = append(initials, owner.Initials)
					}
				}
				opts.OverlayText = strings.Join(initials, " ")
			case "lastname":
				// Collect all owners' last names
				var lastNames []string
				for _, owner := range item.Owners {
					nameParts := strings.Fields(owner.Name)
					if len(nameParts) > 0 {
						lastNames = append(lastNames, nameParts[len(nameParts)-1])
					}
				}
				opts.OverlayText = strings.Join(lastNames, " ")
			}
		}

		qrBase64, err := generateQRCodeBase64(item.ItemCode, opts)
		if err != nil {
			log.Printf("Error generating QR for %s: %v", item.ItemCode, err)
			continue
		}

		url, _ := generateItemURL(item.ItemCode)

		itemsWithQR = append(itemsWithQR, ItemWithQR{
			Item:   item,
			QRCode: qrBase64,
			URL:    url,
		})
		itemIDs = append(itemIDs, item.ID)
	}

	if mark {
		markItemsAsExported(itemIDs)
	}

	data := map[string]interface{}{
		"Items": itemsWithQR,
		"Count": len(itemsWithQR),
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<title>Unexported QR Codes</title>
	<style>
		@media print {
			.no-print { display: none; }
			@page { margin: 0.5in; }
		}
		body {
			font-family: Arial, sans-serif;
			margin: 0;
			padding: 20px;
		}
		.button-bar {
			margin: 20px 0;
			text-align: center;
		}
		button {
			padding: 10px 20px;
			margin: 5px;
			cursor: pointer;
			background: #c30000;
			color: white;
			border: none;
			border-radius: 4px;
		}
		button:hover {
			background: #a00000;
		}
		.qr-grid {
			display: grid;
			grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
			gap: 20px;
			margin: 20px 0;
		}
		.qr-container {
			text-align: center;
			page-break-inside: avoid;
			padding: 15px;
			border: 2px dashed #ccc;
		}
		.item-code {
			font-size: 18px;
			font-weight: bold;
			margin: 10px 0;
		}
		.description {
			color: #666;
			font-size: 12px;
			margin: 5px 0;
		}
		.qr-code {
			margin: 10px auto;
		}
		.url {
			font-size: 10px;
			color: #999;
			word-break: break-all;
			margin: 5px 0;
		}
	</style>
</head>
<body>
	<div class="button-bar no-print">
		<h2>Unexported QR Codes ({{.Count}} items)</h2>
		<button onclick="window.print()">🖨️ Print / Save as PDF</button>
		<button onclick="window.close()">Close</button>
	</div>

	<div class="qr-grid">
		{{range .Items}}
		<div class="qr-container">
			<div class="item-code">{{.ItemCode}}</div>
			{{if .Description}}
			<div class="description">{{.Description}}</div>
			{{end}}
			<div class="qr-code">
				<img src="data:image/png;base64,{{.QRCode}}" alt="QR Code" width="200" height="200">
			</div>
			<div class="url">{{.URL}}</div>
		</div>
		{{end}}
	</div>
</body>
</html>`

	t := template.Must(template.New("qr").Parse(tmpl))
	t.Execute(w, data)
}
