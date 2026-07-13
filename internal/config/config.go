package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultFileName = ".build-brief.json"

type Config struct {
	Matches []Match `json:"matches"`
}

type Match struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

func Load(projectDir, explicitPath string) (Config, string, error) {
	path := strings.TrimSpace(explicitPath)
	if path == "" {
		path = filepath.Join(projectDir, DefaultFileName)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return Config{}, "", nil
			}
			return Config{}, "", fmt.Errorf("stat default config %s: %w", path, err)
		}
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, "", fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := validate(cfg); err != nil {
		return Config{}, "", err
	}

	return cfg, path, nil
}

func validate(cfg Config) error {
	for i, match := range cfg.Matches {
		name := strings.TrimSpace(match.Name)
		if name == "" {
			return fmt.Errorf("matches[%d].name is required", i)
		}
		pattern := strings.TrimSpace(match.Pattern)
		if pattern == "" {
			return fmt.Errorf("match %q pattern is required", name)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("match %q has invalid regex: %w", name, err)
		}
	}
	return nil
}
