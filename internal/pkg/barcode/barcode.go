// Package barcode normalizes product codes (GTIN family barcodes and free-form vendor codes)
// so the same physical code always compares equal regardless of how it was entered.
package barcode

import (
	"strings"
	"unicode"
)

type Type string

const (
	TypeEAN13 Type = "ean13" // also covers UPC-A and GTIN-12/13/14 (canonicalized to 13 digits when possible)
	TypeEAN8  Type = "ean8"
	TypeOther Type = "other" // vendor SKUs, model numbers, anything non-GTIN
)

// Code is a normalized product code.
type Code struct {
	Value string `json:"value"`
	Type  Type   `json:"type"`
}

// Normalize canonicalizes raw input. Digit-only inputs with a valid GTIN check digit are
// canonicalized: UPC-A (12) and GTIN-14 with a zero indicator are converted to EAN-13, and
// UPC-E (8 digits starting with 0/1) that fails EAN-8 validation is expanded to UPC-A first.
// Everything else is upper-cased with whitespace and separators removed.
func Normalize(raw string) Code {
	cleaned := clean(raw)
	if cleaned == "" {
		return Code{Type: TypeOther}
	}

	if isDigits(cleaned) {
		if gtin, typ, ok := canonicalGTIN(cleaned); ok {
			return Code{Value: gtin, Type: typ}
		}
	}

	return Code{Value: cleaned, Type: TypeOther}
}

// clean removes whitespace and common separators and upper-cases the rest.
func clean(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r), r == '-', r == '.', r == '/', r == '_':
			continue
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func canonicalGTIN(digits string) (string, Type, bool) {
	switch len(digits) {
	case 8:
		if ValidCheckDigit(digits) {
			return digits, TypeEAN8, true
		}
		if upca, ok := expandUPCE(digits); ok {
			return "0" + upca, TypeEAN13, true
		}
	case 12:
		if ValidCheckDigit(digits) {
			return "0" + digits, TypeEAN13, true
		}
	case 13:
		if ValidCheckDigit(digits) {
			return digits, TypeEAN13, true
		}
	case 14:
		if ValidCheckDigit(digits) && digits[0] == '0' {
			return digits[1:], TypeEAN13, true
		}
	}
	return "", "", false
}

// ValidCheckDigit verifies the GS1 mod-10 check digit of a digit-only string.
func ValidCheckDigit(digits string) bool {
	if len(digits) < 2 || !isDigits(digits) {
		return false
	}
	return checkDigit(digits[:len(digits)-1]) == digits[len(digits)-1]
}

func checkDigit(body string) byte {
	sum := 0
	// Weights alternate 3,1,... starting from the rightmost digit of the body.
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if (len(body)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

// expandUPCE converts an 8-digit UPC-E (number system, 6 data digits, check digit) to UPC-A.
func expandUPCE(upce string) (string, bool) {
	ns := upce[0]
	if ns != '0' && ns != '1' {
		return "", false
	}
	d := upce[1:7]
	check := upce[7]

	var body string
	switch d[5] {
	case '0', '1', '2':
		body = string(ns) + d[0:2] + string(d[5]) + "0000" + d[2:5]
	case '3':
		body = string(ns) + d[0:3] + "00000" + d[3:5]
	case '4':
		body = string(ns) + d[0:4] + "00000" + string(d[4])
	default:
		body = string(ns) + d[0:5] + "0000" + string(d[5])
	}

	upca := body + string(check)
	if !ValidCheckDigit(upca) {
		return "", false
	}
	return upca, true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
