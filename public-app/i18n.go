package main

import (
	"net/http"
	"strings"
)

// Translations holds all translatable strings for the public app.
// To add a new language, create a new translations() function and register it in the langMap.
type Translations struct {
	// Language metadata
	Code string // e.g. "en", "de"

	// Item page
	ItemFound    string // header title
	ContactInfo  string // section heading
	AlertMessage string // alert box message

	// Button labels
	BtnEmail    string
	BtnCall     string
	BtnSMS      string
	BtnWhatsApp string

	// Message templates — use {item} as placeholder for the item code
	MsgEmailSubject string
	MsgEmailBody    string
	MsgSMSBody      string
	MsgWhatsAppBody string

	// Error page
	ErrTitle   string
	ErrHeading string
	ErrDesc    string

	ErrWhyTitle string
	ErrWhy1     string
	ErrWhy2     string
	ErrWhy3     string
	ErrWhy4     string

	ErrWhatTitle string
	ErrWhat1     string
	ErrWhat2     string
	ErrWhat3     string
	ErrWhat4     string

	ErrURLHint string

	// General
	FooterThanksPrefix string // e.g. "Thanks - powered by" (TRixie will be a link)
	ErrFooterDesc      string // e.g. "Secure Item Recovery System" (TRixie will be a link)
}

// ---------------------------------------------------------------------------
// Language registry
// ---------------------------------------------------------------------------

var langMap = map[string]Translations{
	"en": translationsEN(),
	"de": translationsDE(),
}

const defaultLang = "en"

// ---------------------------------------------------------------------------
// English
// ---------------------------------------------------------------------------

func translationsEN() Translations {
	return Translations{
		Code: "en",

		// Item page
		ItemFound:    "Item found",
		ContactInfo:  "Contact Information",
		AlertMessage: "If you found this item, please contact the owner(s) below:",

		BtnEmail:    "Email",
		BtnCall:     "Call",
		BtnSMS:      "SMS",
		BtnWhatsApp: "WhatsApp",

		MsgEmailSubject: "Found Your Item ({item})",
		MsgEmailBody:    "Hey! I found your item ({item}), contact me :)",
		MsgSMSBody:      "Hey! I found your item ({item})",
		MsgWhatsAppBody: "Hey! I found your item ({item})",

		// Error page
		ErrTitle:   "Access Denied - TRixie",
		ErrHeading: "Access Denied",
		ErrDesc:    "This item page requires a valid signature to access. The QR code you scanned may be invalid or the link is incomplete.",

		ErrWhyTitle: "Why am I seeing this?",
		ErrWhy1:     "The QR code may be damaged or incorrectly printed",
		ErrWhy2:     "The link was manually typed and is missing the signature",
		ErrWhy3:     "The QR code is outdated (database was reset)",
		ErrWhy4:     "Security verification failed",

		ErrWhatTitle: "What should I do?",
		ErrWhat1:     "Try scanning the QR code again carefully",
		ErrWhat2:     "Make sure you're using the complete link from the QR code",
		ErrWhat3:     "Contact the item owner directly if you have their information",
		ErrWhat4:     "If this is your item, generate a new QR code from the admin panel",

		ErrURLHint: "Valid item URLs must include a signature parameter, like:",

		// General
		FooterThanksPrefix: "Thanks - powered by",
		ErrFooterDesc:      "Secure Item Recovery System",
	}
}

// ---------------------------------------------------------------------------
// German
// ---------------------------------------------------------------------------

func translationsDE() Translations {
	return Translations{
		Code: "de",

		// Item page
		ItemFound:    "Gegenstand gefunden",
		ContactInfo:  "Kontaktinformationen",
		AlertMessage: "Wenn Sie diesen Gegenstand gefunden haben, kontaktieren Sie bitte den/die Eigentümer:",

		BtnEmail:    "E-Mail",
		BtnCall:     "Anrufen",
		BtnSMS:      "SMS",
		BtnWhatsApp: "WhatsApp",

		MsgEmailSubject: "Gegenstand gefunden ({item})",
		MsgEmailBody:    "Hallo! Ich habe Ihren Gegenstand ({item}) gefunden, kontaktieren Sie mich :)",
		MsgSMSBody:      "Hallo! Ich habe Ihren Gegenstand ({item}) gefunden",
		MsgWhatsAppBody: "Hallo! Ich habe Ihren Gegenstand ({item}) gefunden",

		// Error page
		ErrTitle:   "Zugriff verweigert - TRixie",
		ErrHeading: "Zugriff verweigert",
		ErrDesc:    "Diese Seite erfordert eine gültige Signatur. Der gescannte QR-Code ist möglicherweise ungültig oder der Link ist unvollständig.",

		ErrWhyTitle: "Warum sehe ich das?",
		ErrWhy1:     "Der QR-Code ist möglicherweise beschädigt oder falsch gedruckt",
		ErrWhy2:     "Der Link wurde manuell eingegeben und die Signatur fehlt",
		ErrWhy3:     "Der QR-Code ist veraltet (Datenbank wurde zurückgesetzt)",
		ErrWhy4:     "Sicherheitsüberprüfung fehlgeschlagen",

		ErrWhatTitle: "Was soll ich tun?",
		ErrWhat1:     "Versuchen Sie, den QR-Code erneut sorgfältig zu scannen",
		ErrWhat2:     "Stellen Sie sicher, dass Sie den vollständigen Link des QR-Codes verwenden",
		ErrWhat3:     "Kontaktieren Sie den Eigentümer direkt, wenn Sie dessen Kontaktdaten haben",
		ErrWhat4:     "Wenn dies Ihr Gegenstand ist, erstellen Sie einen neuen QR-Code im Admin-Bereich",

		ErrURLHint: "Gültige URLs müssen einen Signaturparameter enthalten, wie:",
		// General
		FooterThanksPrefix: "Danke - bereitgestellt von",
		ErrFooterDesc:      "Sicheres Fundsachen-System",
	}
}

// ---------------------------------------------------------------------------
// Accept-Language detection
// ---------------------------------------------------------------------------

// detectLanguage parses the Accept-Language header and returns the best
// matching Translations. Falls back to English when no match is found.
func detectLanguage(r *http.Request) Translations {
	code := negotiateLanguage(r.Header.Get("Accept-Language"))
	if t, ok := langMap[code]; ok {
		return t
	}
	return langMap[defaultLang]
}

// negotiateLanguage picks the highest-priority language code from the
// Accept-Language header value that we have a translation for.
// Returns defaultLang when nothing matches.
//
// Supports formats like:
//
//	"de"
//	"de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7"
//	"en-US,en;q=0.9"
func negotiateLanguage(header string) string {
	if header == "" {
		return defaultLang
	}

	// Parse entries and sort by quality (we iterate in order, first match wins
	// when qualities are omitted — browsers send them in priority order).
	type entry struct {
		lang string
		q    float64
	}

	var entries []entry
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		lang := part
		q := 1.0

		if idx := strings.Index(part, ";"); idx != -1 {
			lang = strings.TrimSpace(part[:idx])
			qPart := strings.TrimSpace(part[idx+1:])
			if strings.HasPrefix(qPart, "q=") {
				var parsed float64
				if _, err := parseFloat(qPart[2:]); err == nil {
					parsed = mustParseFloat(qPart[2:])
					q = parsed
				}
			}
		}

		lang = strings.ToLower(lang)
		entries = append(entries, entry{lang: lang, q: q})
	}

	// Sort by quality descending (stable so equal q keeps header order)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].q > entries[i].q {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Match: try exact, then base language (e.g. "de-DE" -> "de")
	for _, e := range entries {
		if _, ok := langMap[e.lang]; ok {
			return e.lang
		}
		if base := baseLanguage(e.lang); base != e.lang {
			if _, ok := langMap[base]; ok {
				return base
			}
		}
	}

	return defaultLang
}

// baseLanguage extracts the primary language subtag: "de-DE" -> "de"
func baseLanguage(tag string) string {
	if idx := strings.IndexByte(tag, '-'); idx != -1 {
		return tag[:idx]
	}
	return tag
}

// parseFloat is a thin wrapper for validation without importing strconv at the top level.
func parseFloat(s string) (float64, error) {
	// Simple float parser for quality values (0-1 range)
	var result float64
	var decimal float64
	pastDot := false
	divisor := 1.0

	for _, c := range s {
		if c == '.' {
			if pastDot {
				return 0, errInvalidFloat
			}
			pastDot = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errInvalidFloat
		}
		digit := float64(c - '0')
		if pastDot {
			divisor *= 10
			decimal += digit / divisor
		} else {
			result = result*10 + digit
		}
	}
	return result + decimal, nil
}

func mustParseFloat(s string) float64 {
	v, _ := parseFloat(s)
	return v
}

var errInvalidFloat = &invalidFloatError{}

type invalidFloatError struct{}

func (e *invalidFloatError) Error() string { return "invalid float" }

// ---------------------------------------------------------------------------
// Message helpers
// ---------------------------------------------------------------------------

// FormatMessage replaces the {item} placeholder with the given item code.
// Returns plain text — Go's html/template contextual auto-escaping handles
// URL percent-encoding when the value appears inside an href attribute.
func FormatMessage(tmpl string, itemCode string) string {
	return strings.ReplaceAll(tmpl, "{item}", itemCode)
}

// MessageStrings holds pre-computed plain-text message strings for use in
// mailto:, sms:, and wa.me links. Go's template engine will URL-encode
// them automatically in the href context.
type MessageStrings struct {
	EmailSubject string
	EmailBody    string
	SMSBody      string
	WhatsAppBody string
}

// BuildMessageStrings computes the plain-text message strings for a given
// set of translations and item code.
func BuildMessageStrings(t Translations, itemCode string) MessageStrings {
	return MessageStrings{
		EmailSubject: FormatMessage(t.MsgEmailSubject, itemCode),
		EmailBody:    FormatMessage(t.MsgEmailBody, itemCode),
		SMSBody:      FormatMessage(t.MsgSMSBody, itemCode),
		WhatsAppBody: FormatMessage(t.MsgWhatsAppBody, itemCode),
	}
}
