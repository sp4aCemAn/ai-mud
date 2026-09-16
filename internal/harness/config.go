package harness

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all harness settings. Loaded from a YAML config file so
// the "game master" can be re-aimed (different model, server, persona)
// without recompiling.
type Config struct {
	// Enabled gates the whole harness; off = the component exits immediately.
	Enabled bool `yaml:"enabled"`

	// BaseURL of an OpenAI-compatible server, including the /v1 prefix.
	// Examples (all local):
	//   http://localhost:11434/v1  (Ollama)
	//   http://localhost:1234/v1   (LM Studio — the GM persona stage)
	//   http://localhost:8000/v1   (vLLM)
	BaseURL string `yaml:"base_url"`

	// InterpreterBaseURL / InterpreterModel: the tool-call stage's own
	// provider (the person an interpreter are SEPARATE by design — the
	// persona turns lore; the interpreter turns decisions into tool
	// calls). Empty = same endpoint/model as the persona (single
	// backend runs).
	InterpreterBaseURL string `yaml:"interpreter_base_url"`
	InterpreterModel   string `yaml:"interpreter_model"`

	// APIKey is sent as a Bearer token. Most local servers ignore it.
	APIKey string `yaml:"api_key"`

	// Model to request by default (must exist on the server, see /v1/models).
	Model string `yaml:"model"`

	// Timeout for HTTP calls, e.g. "30s" or "2m".
	Timeout string `yaml:"timeout"`

	// Default sampling parameters used when a request doesn't override them.
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`

	// Cadence is how often the game-master loop surveys the world
	// (e.g. "45s", "2m"). The default (45s) is intentionally slow — a
	// quiet world shouldn't burn local LLM budget.
	Cadence string `yaml:"cadence"`

	// Cooldown is the post-turn quiet window (the poke-storm rail: an
	// exploring player triggers frontier turns; the shortest interval
	// between two full model turns). Default "15s" — tuned for dense
	// world-building expoloration.
	Cooldown string `yaml:"cooldown"`

	// ToolsPath points at the verb card (OpenAI function-calling JSON;
	// the interpreter stage's toolset). Empty/missing = the single-stage
	// legacy loop (text protocol only — the CI mock's degradation path).
	ToolsPath string `yaml:"tools_path"`

	// SystemPrompt is the seed of the game-master persona. The loop
	// appends the one-JSON-object tool protocol off this seed.
	SystemPrompt string `yaml:"system_prompt"`
}

// ConfigPath resolves the config file location: $HARNESS_CONFIG or the
// default configs/harness.yaml (relative to the working directory).
func ConfigPath() string {
	if p := os.Getenv("HARNESS_CONFIG"); p != "" {
		return p
	}
	return "configs/harness.yaml"
}

// LoadConfig reads and validates the config file. Defaults are applied
// first so the file only needs to override what it cares about.
func LoadConfig(path string) (Config, error) {
	cfg := Config{
		Enabled:     false,
		BaseURL:     "http://localhost:11434/v1",
		Model:       "",
		Timeout:     "30s",
		Temperature: 0.7,
		MaxTokens:   512,
		Cadence:     "45s",
		ToolsPath:   "configs/gm_tools.json",
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.BaseURL == "" {
		return cfg, fmt.Errorf("config %s: base_url is required", path)
	}
	if _, err := timeoutDuration(cfg.Timeout); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if v := os.Getenv("HARNESS_COOLDOWN"); v != "" {
		cfg.Cooldown = v
	}
	if _, err := time.ParseDuration(cfg.Cooldown); cfg.Cooldown != "" && err != nil {
		return cfg, fmt.Errorf("config %s: invalid cooldown %q", path, cfg.Cooldown)
	}

	// env overrides — lets e.g. Docker aim at a different host without
	// editing the shared config file (see build/compose.yaml)
	if v := os.Getenv("HARNESS_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("HARNESS_MODEL"); v != "" {
		cfg.Model = v
	}
	// the interpreter stage's own provider (empty = same backend)
	if v := os.Getenv("HARNESS_INTERPRETER_BASE_URL"); v != "" {
		cfg.InterpreterBaseURL = v
	}
	if v := os.Getenv("HARNESS_INTERPRETER_MODEL"); v != "" {
		cfg.InterpreterModel = v
	}
	return cfg, nil
}

// timeoutDuration parses cfg.Timeout, falling back to 30s when empty.
func timeoutDuration(s string) (time.Duration, error) {
	if s == "" {
		return 30 * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid timeout %q (use e.g. \"30s\", \"2m\")", s)
	}
	return d, nil
}
