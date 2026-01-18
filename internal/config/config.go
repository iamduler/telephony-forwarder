package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Server ServerConfig `yaml:"server"`
	NATS   NATSConfig   `yaml:"nats"`
	Routes []Route      `yaml:"routes"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port         int `yaml:"port"`
	ReadTimeout  int `yaml:"read_timeout_seconds"`
	WriteTimeout int `yaml:"write_timeout_seconds"`
}

// NATSConfig holds NATS connection configuration
type NATSConfig struct {
	URL            string `yaml:"url"`
	StreamName     string `yaml:"stream_name"`
	SubjectPattern string `yaml:"subject_pattern"`
	AckWait        int    `yaml:"ack_wait_seconds"`
	MaxDeliveries  int    `yaml:"max_deliveries"`
}

// Route maps a domain to backend endpoints
type Route struct {
	Domain    string   `yaml:"domain" json:"domain"`
	Endpoints []string `yaml:"endpoints" json:"endpoints"`
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	if c.Server.Port <= 0 {
		return fmt.Errorf("server port must be positive")
	}

	if c.NATS.URL == "" {
		return fmt.Errorf("nats url is required")
	}

	if c.NATS.StreamName == "" {
		return fmt.Errorf("nats stream_name is required")
	}

	if c.NATS.SubjectPattern == "" {
		return fmt.Errorf("nats subject_pattern is required")
	}

	if c.NATS.AckWait <= 0 {
		return fmt.Errorf("nats ack_wait_seconds must be positive")
	}

	if c.NATS.MaxDeliveries <= 0 {
		return fmt.Errorf("nats max_deliveries must be positive")
	}

	// Validate that ack_wait is greater than backend timeout (3 seconds)
	if c.NATS.AckWait <= 3 {
		return fmt.Errorf("nats ack_wait_seconds (%d) must be greater than backend timeout (3 seconds)", c.NATS.AckWait)
	}

	return nil
}

// GetEndpoints returns the list of endpoints for a given domain
func (c *Config) GetEndpoints(domain string) []string {
	for _, route := range c.Routes {
		if route.Domain == domain {
			return route.Endpoints
		}
	}
	return nil
}

