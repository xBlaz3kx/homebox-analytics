package barcode

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Code
	}{
		{name: "ean13", in: "4006381333931", want: Code{Value: "4006381333931", Type: TypeEAN13}},
		{name: "ean13 with spaces", in: " 4 006381 333931 ", want: Code{Value: "4006381333931", Type: TypeEAN13}},
		{name: "upc-a to ean13", in: "036000291452", want: Code{Value: "0036000291452", Type: TypeEAN13}},
		{name: "gtin14 zero indicator", in: "00036000291452", want: Code{Value: "0036000291452", Type: TypeEAN13}},
		{name: "ean8", in: "96385074", want: Code{Value: "96385074", Type: TypeEAN8}},
		{name: "upc-e expands to upc-a", in: "04252614", want: Code{Value: "0042100005264", Type: TypeEAN13}},
		{name: "bad check digit is other", in: "4006381333932", want: Code{Value: "4006381333932", Type: TypeOther}},
		{name: "vendor sku", in: "ab-123/x", want: Code{Value: "AB123X", Type: TypeOther}},
		{name: "empty", in: "  ", want: Code{Type: TypeOther}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Normalize(tt.in))
		})
	}
}

func TestValidCheckDigit(t *testing.T) {
	assert.True(t, ValidCheckDigit("4006381333931"))
	assert.True(t, ValidCheckDigit("036000291452"))
	assert.False(t, ValidCheckDigit("036000291453"))
	assert.False(t, ValidCheckDigit("12a"))
}
