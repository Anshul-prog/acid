// #file: masking.go
// #package: dbsearch
// #purpose: PII (Personally Identifiable Information) masking utilities.
//
// All sensitive identity numbers are masked by default in API responses.
// Only users whose JWT contains the "unmask" scope (or role == "admin" with
// explicit unmask permission) receive the raw values.
//
// Masking rules:
//   Aadhaar    XXXX-XXXX-1234   (last 4 visible)
//   PAN        XXXXX1234F       (last 5 visible)
//   Passport   XXXXXXX1         (last 2 visible)
//   DL         XXXXXXXXXX234    (last 3 visible)
//   Phone      XXXXXX3210       (last 4 visible)
//   Bank acct  XXXXXX1234       (last 4 visible)
//   Email      j***@example.com (username truncated)

package dbsearch

import (
	"strings"
)

// MaskAadhaar masks a 12-digit Aadhaar number.
// Input may include spaces or dashes; output is always XXXX-XXXX-NNNN.
//
// #pii-masking
func MaskAadhaar(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "-", "")
	if len(s) < 4 {
		return "XXXX-XXXX-" + s
	}
	return "XXXX-XXXX-" + s[len(s)-4:]
}

// MaskPAN masks a 10-character PAN card number.
// Output: XXXXX + last 5 characters.
//
// #pii-masking
func MaskPAN(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 5 {
		return "XXXXX" + s
	}
	return "XXXXX" + s[5:]
}

// MaskPassport masks a passport number — shows last 2 characters.
//
// #pii-masking
func MaskPassport(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 2 {
		return strings.Repeat("X", 7) + s
	}
	prefix := strings.Repeat("X", len(s)-2)
	return prefix + s[len(s)-2:]
}

// MaskDrivingLicense masks a driving licence number — shows last 3 characters.
//
// #pii-masking
func MaskDrivingLicense(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 3 {
		return strings.Repeat("X", 10) + s
	}
	prefix := strings.Repeat("X", len(s)-3)
	return prefix + s[len(s)-3:]
}

// MaskPhone masks a phone number — shows last 4 digits.
//
// #pii-masking
func MaskPhone(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return "XXXXXX" + s
	}
	return strings.Repeat("X", len(s)-4) + s[len(s)-4:]
}

// MaskBankAccount masks a bank account number — shows last 4 digits.
//
// #pii-masking
func MaskBankAccount(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return "XXXXXX" + s
	}
	return strings.Repeat("X", len(s)-4) + s[len(s)-4:]
}

// MaskEmail masks an email address — keeps first char + domain.
//
// #pii-masking
func MaskEmail(s string) string {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "***@***"
	}
	user := s[:at]
	domain := s[at:]
	if len(user) == 1 {
		return user + "***" + domain
	}
	return string(user[0]) + strings.Repeat("*", len(user)-1) + domain
}

// MaskDocNumber applies the correct masking function based on the document type.
// The docType values match the CHECK constraint in entity_documents:
//   "aadhaar" | "pan" | "passport" | "driving_license" | "voter_id" | "other"
//
// #pii-masking: called by the entity repository when maskPII == true.
func MaskDocNumber(docType, docNumber string) string {
	switch strings.ToLower(docType) {
	case "aadhaar":
		return MaskAadhaar(docNumber)
	case "pan":
		return MaskPAN(docNumber)
	case "passport":
		return MaskPassport(docNumber)
	case "driving_license":
		return MaskDrivingLicense(docNumber)
	default:
		return MaskPassport(docNumber) // generic: last 2 visible
	}
}
