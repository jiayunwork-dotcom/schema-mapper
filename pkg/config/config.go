package config

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultOutputFormat string `yaml:"default_output_format,omitempty"`
	Concurrency         int    `yaml:"concurrency,omitempty"`
	StreamThreshold     int64  `yaml:"stream_threshold,omitempty"`
	MemoryLimit         int64  `yaml:"memory_limit,omitempty"`
	LogLevel            string `yaml:"log_level,omitempty"`
	ColorMode           string `yaml:"color_mode,omitempty"`
	ProgressBar         bool   `yaml:"progress_bar,omitempty"`
}

const configFileName = ".schema-mapper.yaml"

func LoadConfig() (*Config, error) {
	cfg := &Config{
		DefaultOutputFormat: "json",
		Concurrency:         4,
		StreamThreshold:     100 * 1024 * 1024,
		MemoryLimit:         256 * 1024 * 1024,
		LogLevel:            "info",
		ColorMode:           "auto",
		ProgressBar:         true,
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(homeDir, configFileName)
		if _, err := os.Stat(path); err == nil {
			if err := loadConfigFile(path, cfg); err != nil {
				return cfg, err
			}
		}
	}

	cwd, err := os.Getwd()
	if err == nil {
		path := filepath.Join(cwd, configFileName)
		if _, err := os.Stat(path); err == nil {
			if err := loadConfigFile(path, cfg); err != nil {
				return cfg, err
			}
		}
	}

	return cfg, nil
}

func loadConfigFile(path string, cfg *Config) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, cfg)
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}
