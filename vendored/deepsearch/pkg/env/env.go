package env

import (
	"log/slog"
	"time"
)

// Env is the authoritative runtime configuration shape.
//
//go:generate go tool envdoc -types="*" -format dotenv -output ../../.env.example
type Env struct {
	// Logging Configuration

	StdLogLevel slog.Level `env:"STD_LOG_LEVEL" envDefault:"info"` // Minimum log level for standard output (console).

	// Model Configuration

	ModelApiKey      string `env:"MODEL_API_KEY" envDefault:""`  // Model API key used for every search request.
	ModelBaseURL     string `env:"MODEL_BASE_URL" envDefault:""` // Model API base URL.
	Model            string `env:"MODEL" envDefault:""`          // Model ID that answers every search request.
	ModelOutputLimit int64  `env:"MODEL_OUTPUT_LIMIT"`           // Maximum output tokens of one search answer.

	// Web Search Configuration

	WebSearchMaxUses int64 `env:"WEB_SEARCH_MAX_USES" envDefault:"10"` // Maximum number of web_search server-tool uses per request.

	// WebFetch Configuration

	WebFetchEnabled  bool          `env:"WEB_FETCH_ENABLED" envDefault:"true"`      // Registers the WebFetch tool, which reads one public web page per call with local and reserved networks blocked.
	WebFetchTimeout  time.Duration `env:"WEB_FETCH_TIMEOUT" envDefault:"15s"`       // Timeout of one WebFetch request, including DNS, dialing, redirects and body read.
	WebFetchMaxBytes int           `env:"WEB_FETCH_MAX_BYTES" envDefault:"2097152"` // Maximum bytes read from a fetched page before it is cut off and marked as truncated (default 2 MiB).
	WebFetchMaxChars int           `env:"WEB_FETCH_MAX_CHARS" envDefault:"20000"`   // Maximum characters the WebFetch tool returns to the model.

	// HTTP Server Configuration

	HttpAddr  string `env:"HTTP_ADDR" envDefault:"0.0.0.0:8231"` // Listen address of the REST API and the MCP endpoint.
	AuthToken string `env:"AUTH_TOKEN" envDefault:""`            // Optional bearer token shared by the REST API and the MCP endpoint; when set every request except /healthz requires it.

	MaxHeaderBytes     int           `env:"MAX_HEADER_BYTES" envDefault:"2097152"`      // Maximum allowed size for HTTP request headers (in bytes).
	MaxMultipartMemory int64         `env:"MAX_MULTIPART_MEMORY" envDefault:"67108864"` // Maximum memory allowed for parsing multipart forms before spilling to disk.
	ReadTimeout        time.Duration `env:"READ_TIMEOUT" envDefault:"120s"`             // Maximum duration for reading an entire HTTP request, including the body.
	WriteTimeout       time.Duration `env:"WRITE_TIMEOUT" envDefault:"300s"`            // Maximum duration before timing out response writes; it must cover a whole search.
	IdleTimeout        time.Duration `env:"IDLE_TIMEOUT" envDefault:"30s"`              // Maximum amount of time to keep idle connections open.
}
