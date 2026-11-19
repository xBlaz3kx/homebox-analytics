package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	HTTP          HTTP          `mapstructure:"http"`
	MCP           MCP           `mapstructure:"mcp"`
	Homebox       Homebox       `mapstructure:"homebox"`
	Postgres      Postgres      `mapstructure:"postgres"`
	Qdrant        Qdrant        `mapstructure:"qdrant"`
	Embeddings    Embeddings    `mapstructure:"embeddings"`
	Sync          Sync          `mapstructure:"sync"`
	Matching      Matching      `mapstructure:"matching"`
	Observability Observability `mapstructure:"observability"`
}

type HTTP struct {
	Address         string        `mapstructure:"address"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// MCP serves the Model Context Protocol endpoint on the HTTP server.
type MCP struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

type Homebox struct {
	URL      string `mapstructure:"url"`
	APIKey   string `mapstructure:"api_key"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	// CodeFields are the custom field names whose values are treated as product codes.
	CodeFields []string      `mapstructure:"code_fields"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

type Postgres struct {
	DSN string `mapstructure:"dsn"`
}

type Qdrant struct {
	Host       string `mapstructure:"host"`
	Port       int    `mapstructure:"port"`
	APIKey     string `mapstructure:"api_key"`
	UseTLS     bool   `mapstructure:"use_tls"`
	Collection string `mapstructure:"collection"`
}

// Embeddings configures an OpenAI-compatible /v1/embeddings endpoint (Ollama, Bifrost, OpenAI, ...).
type Embeddings struct {
	BaseURL    string        `mapstructure:"base_url"`
	APIKey     string        `mapstructure:"api_key"`
	Model      string        `mapstructure:"model"`
	Dimensions int           `mapstructure:"dimensions"`
	BatchSize  int           `mapstructure:"batch_size"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

type Sync struct {
	Interval time.Duration `mapstructure:"interval"`
}

type Matching struct {
	// MinSimilarity is the minimum cosine similarity for a hybrid match to be considered confident.
	MinSimilarity float64 `mapstructure:"min_similarity"`
	// MinMargin is the minimum similarity lead of the top candidate over the runner-up.
	MinMargin float64 `mapstructure:"min_margin"`
}

type Observability struct {
	Enabled     bool    `mapstructure:"enabled"`
	ServiceName string  `mapstructure:"service_name"`
	Environment string  `mapstructure:"environment"`
	Endpoint    string  `mapstructure:"endpoint"`
	Protocol    string  `mapstructure:"protocol"`
	Insecure    bool    `mapstructure:"insecure"`
	SampleRatio float64 `mapstructure:"sample_ratio"`
}

func (c *Config) Validate() error {
	var errs []error
	if c.Homebox.URL == "" {
		errs = append(errs, errors.New("homebox.url is required"))
	}
	if c.Homebox.APIKey == "" && (c.Homebox.Username == "" || c.Homebox.Password == "") {
		errs = append(errs, errors.New("homebox.api_key or homebox.username+password is required"))
	}
	if c.MCP.Enabled && (!strings.HasPrefix(c.MCP.Path, "/") || strings.HasPrefix(c.MCP.Path, "/api/") || c.MCP.Path == "/") {
		errs = append(errs, errors.New("mcp.path must start with / and not collide with the REST API"))
	}
	if c.Postgres.DSN == "" {
		errs = append(errs, errors.New("postgres.dsn is required"))
	}
	if c.Embeddings.Model == "" {
		errs = append(errs, errors.New("embeddings.model is required"))
	}
	if c.Sync.Interval <= 0 {
		errs = append(errs, errors.New("sync.interval must be positive"))
	}
	return errors.Join(errs...)
}

func setDefaults(v *viper.Viper) {
	// Keys without a meaningful default are still registered so AutomaticEnv picks them up on Unmarshal.
	for _, key := range []string{"homebox.api_key", "homebox.username", "homebox.password", "qdrant.api_key", "qdrant.use_tls", "embeddings.api_key", "embeddings.dimensions", "observability.endpoint"} {
		v.SetDefault(key, nil)
	}

	v.SetDefault("http.address", ":8080")
	v.SetDefault("http.shutdown_timeout", 15*time.Second)
	v.SetDefault("mcp.enabled", true)
	v.SetDefault("mcp.path", "/mcp")
	v.SetDefault("homebox.url", "http://localhost:7745")
	v.SetDefault("homebox.code_fields", []string{"EAN", "UPC", "Barcode", "GTIN", "SKU"})
	v.SetDefault("homebox.timeout", 15*time.Second)
	v.SetDefault("postgres.dsn", "postgres://homebox:homebox@localhost:5432/homebox_analytics?sslmode=disable")
	v.SetDefault("qdrant.host", "localhost")
	v.SetDefault("qdrant.port", 6334)
	v.SetDefault("qdrant.collection", "homebox_items")
	v.SetDefault("embeddings.base_url", "http://localhost:11434/v1")
	v.SetDefault("embeddings.model", "nomic-embed-text")
	v.SetDefault("embeddings.batch_size", 32)
	v.SetDefault("embeddings.timeout", 60*time.Second)
	v.SetDefault("sync.interval", 10*time.Minute)
	v.SetDefault("matching.min_similarity", 0.75)
	v.SetDefault("matching.min_margin", 0.03)
	v.SetDefault("observability.enabled", false)
	v.SetDefault("observability.service_name", "homebox-analytics")
	v.SetDefault("observability.environment", "dev")
	v.SetDefault("observability.protocol", "grpc")
	v.SetDefault("observability.insecure", true)
	v.SetDefault("observability.sample_ratio", 1.0)
}

// loadConfig reads configuration from the optional file and HBA_-prefixed environment variables
// (e.g. HBA_HOMEBOX_API_KEY overrides homebox.api_key).
func loadConfig(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("HBA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	return &cfg, cfg.Validate()
}
