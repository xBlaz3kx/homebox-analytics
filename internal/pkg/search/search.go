// Package search provides domain-agnostic hybrid (dense + sparse) retrieval over an Index.
package search

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Embedder turns texts into dense vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// SparseVector is a term-frequency vector keyed by hashed term ID.
type SparseVector struct {
	Indices []uint32
	Values  []float32
}

// Document is an indexable unit. Payload is stored alongside the vectors and returned with hits.
type Document struct {
	ID      string
	Text    string
	Payload map[string]any
}

// Point is a Document with its vectors computed.
type Point struct {
	ID      string
	Dense   []float32
	Sparse  SparseVector
	Payload map[string]any
}

// Filter restricts results to points whose payload field matches any of the given keywords.
type Filter struct {
	Field string
	Any   []string
}

// Hit is a scored search result. Score is the fused rank score (comparable only within one
// query). Similarity is the cosine similarity between the query and the hit's dense vector,
// set by HybridSearch when the index returns vectors; use it for absolute confidence decisions.
type Hit struct {
	ID         string
	Score      float64
	Similarity float64
	Payload    map[string]any
	Vector     []float32
}

// Index stores points and runs fused dense+sparse queries.
type Index interface {
	Upsert(ctx context.Context, points []Point) error
	Delete(ctx context.Context, ids []string) error
	// SetPayload merges payload fields into an existing point without touching its vectors.
	SetPayload(ctx context.Context, id string, payload map[string]any) error
	Query(ctx context.Context, dense []float32, sparse SparseVector, filter *Filter, limit int) ([]Hit, error)
	// Scroll returns all points matching the filter (nil = all), payload only.
	Scroll(ctx context.Context, filter *Filter) ([]Hit, error)
}

// HybridSearch combines an Embedder, a sparse Encoder and an Index.
type HybridSearch struct {
	embedder Embedder
	encoder  *Encoder
	index    Index
}

func NewHybridSearch(embedder Embedder, encoder *Encoder, index Index) *HybridSearch {
	return &HybridSearch{embedder: embedder, encoder: encoder, index: index}
}

// Index embeds and upserts documents.
func (h *HybridSearch) Index(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}

	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}

	dense, err := h.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed documents: %w", err)
	}
	if len(dense) != len(docs) {
		return fmt.Errorf("embedder returned %d vectors for %d documents", len(dense), len(docs))
	}

	points := make([]Point, len(docs))
	for i, d := range docs {
		points[i] = Point{ID: d.ID, Dense: dense[i], Sparse: h.encoder.Encode(d.Text), Payload: d.Payload}
	}

	return h.index.Upsert(ctx, points)
}

// Search runs a hybrid query for text.
func (h *HybridSearch) Search(ctx context.Context, text string, filter *Filter, limit int) ([]Hit, error) {
	if text == "" {
		return nil, errors.New("empty search text")
	}

	dense, err := h.embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(dense) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for 1 query", len(dense))
	}

	hits, err := h.index.Query(ctx, dense[0], h.encoder.Encode(text), filter, limit)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		if len(hits[i].Vector) > 0 {
			hits[i].Similarity = Cosine(dense[0], hits[i].Vector)
		}
	}
	return hits, nil
}

// Cosine returns the cosine similarity of two equal-length vectors (0 if either is zero).
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
