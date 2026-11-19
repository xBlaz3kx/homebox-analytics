// Package embeddings is a client for OpenAI-compatible /v1/embeddings endpoints
// (Ollama, Bifrost, OpenAI, vLLM, LocalAI, ...).
package embeddings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	BatchSize  int
	HTTPClient *http.Client
}

type Client struct {
	api       openai.Client
	model     string
	dims      int
	batchSize int
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, errors.New("embeddings: base URL and model are required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport,
			otelhttp.WithSpanOptions(trace.WithAttributes(semconv.ServicePeerName("embeddings"))))}
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		// Ollama ignores the key, but the SDK always sends an Authorization header.
		apiKey = "unused"
	}

	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 32
	}

	return &Client{
		api: openai.NewClient(
			option.WithBaseURL(cfg.BaseURL),
			option.WithAPIKey(apiKey),
			option.WithHTTPClient(httpClient),
			option.WithMaxRetries(2),
		),
		model:     cfg.Model,
		dims:      cfg.Dimensions,
		batchSize: batch,
	}, nil
}

// Model returns the configured embedding model name.
func (c *Client) Model() string {
	return c.model
}

// Embed returns one vector per input text, in input order, batching requests.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += c.batchSize {
		end := min(start+c.batchSize, len(texts))
		vecs, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	params := openai.EmbeddingNewParams{
		Input:          openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model:          openai.EmbeddingModel(c.model),
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	}
	if c.dims > 0 {
		params.Dimensions = openai.Int(int64(c.dims))
	}

	resp, err := c.api.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("create embeddings: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(resp.Data), len(texts))
	}

	data := resp.Data
	sort.Slice(data, func(i, j int) bool { return data[i].Index < data[j].Index })

	out := make([][]float32, len(data))
	for i, d := range data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		out[i] = v
	}
	return out, nil
}

// Dimension probes the endpoint once and returns the vector size.
func (c *Client) Dimension(ctx context.Context) (int, error) {
	vecs, err := c.Embed(ctx, []string{"dimension probe"})
	if err != nil {
		return 0, err
	}
	return len(vecs[0]), nil
}

// Ping checks the endpoint is reachable by listing models (supported by Ollama, Bifrost and OpenAI).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.Models.List(ctx)
	return err
}
