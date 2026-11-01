package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncoderTokens(t *testing.T) {
	e := NewEncoder()

	assert.Equal(t, []string{"500g", "barilla", "spaghetti", "5"}, e.Tokens("Barilla Spaghetti n.5 500g"))
	assert.Equal(t, []string{"500g", "barilla", "spag"}, e.Tokens("BARILLA SPAG 0,5 KG"))
	assert.Equal(t, []string{"1500ml", "water"}, e.Tokens("Water 1.5L"))
	assert.Equal(t, []string{"4", "aa", "batteries"}, e.Tokens("4 x AA batteries"))
}

func TestEncoderEncode(t *testing.T) {
	e := NewEncoder()

	v := e.Encode("milk milk 1l")
	assert.Len(t, v.Indices, 2)
	assert.Len(t, v.Values, 2)
	assert.IsIncreasing(t, v.Indices)

	byID := map[uint32]float32{}
	for i, id := range v.Indices {
		byID[id] = v.Values[i]
	}
	assert.Equal(t, float32(2), byID[termID("milk")])
	assert.Equal(t, float32(1), byID[termID("1000ml")])

	// Equivalent quantities share a term.
	assert.Equal(t, e.Encode("1000 ml"), e.Encode("1 l"))
}
