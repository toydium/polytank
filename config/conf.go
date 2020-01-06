package config

import (
	"os"

	"github.com/go-yaml/yaml"
)

type Config struct {
	Concurrency uint32            `yaml:"concurrency"`
	Timeout     uint32            `yaml:"timeout"`
	MaxCount    uint32            `yaml:"max_count"`
	MaxSeconds  uint32            `yaml:"max_seconds"`
	ExMap       map[string]string `yaml:"ex_map"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(f)
	var conf Config
	if err := dec.Decode(&conf); err != nil {
		return nil, err
	}
	return &conf, nil
}
