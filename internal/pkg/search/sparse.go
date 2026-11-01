package search

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Encoder produces term-frequency sparse vectors. IDF weighting is left to the index
// (e.g. Qdrant's `modifier: idf`), which makes the combination behave like BM25.
type Encoder struct {
	stopwords map[string]struct{}
}

func NewEncoder() *Encoder {
	stop := map[string]struct{}{}
	for _, w := range []string{"a", "an", "and", "the", "of", "for", "with", "in", "on", "to", "x"} {
		stop[w] = struct{}{}
	}
	return &Encoder{stopwords: stop}
}

// quantityRe matches a number immediately followed by a unit, e.g. "500g", "0,5 kg", "1.5L".
var quantityRe = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(kg|g|mg|l|ml|cl|dl|pcs|pc|kos|m|cm|mm)\b`)

// unitScale converts units to a base unit so "0.5kg" and "500g" produce the same token.
var unitScale = map[string]struct {
	base  string
	scale float64
}{
	"kg": {"g", 1000}, "g": {"g", 1}, "mg": {"g", 0.001},
	"l": {"ml", 1000}, "dl": {"ml", 100}, "cl": {"ml", 10}, "ml": {"ml", 1},
	"m": {"mm", 1000}, "cm": {"mm", 10}, "mm": {"mm", 1},
	"pcs": {"pcs", 1}, "pc": {"pcs", 1}, "kos": {"pcs", 1},
}

// Tokens returns the normalized tokens for text.
func (e *Encoder) Tokens(text string) []string {
	text = strings.ToLower(text)

	var tokens []string
	text = quantityRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := quantityRe.FindStringSubmatch(m)
		n, err := strconv.ParseFloat(strings.ReplaceAll(parts[1], ",", "."), 64)
		if u, ok := unitScale[strings.ToLower(parts[2])]; ok && err == nil {
			tokens = append(tokens, strconv.FormatFloat(n*u.scale, 'f', -1, 64)+u.base)
		}
		return " "
	})

	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if _, stop := e.stopwords[w]; stop || len([]rune(w)) < 2 && !isNumber(w) {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// Encode returns the term-frequency sparse vector for text with indices sorted ascending.
func (e *Encoder) Encode(text string) SparseVector {
	counts := map[uint32]float32{}
	for _, t := range e.Tokens(text) {
		counts[termID(t)]++
	}

	v := SparseVector{Indices: make([]uint32, 0, len(counts)), Values: make([]float32, 0, len(counts))}
	for id := range counts {
		v.Indices = append(v.Indices, id)
	}
	sort.Slice(v.Indices, func(i, j int) bool { return v.Indices[i] < v.Indices[j] })
	for _, id := range v.Indices {
		v.Values = append(v.Values, counts[id])
	}
	return v
}

func termID(term string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(term))
	return h.Sum32()
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}
