package main

import (
	"bytes"
	"github.com/pelletier/go-toml"
	"reflect"
	"testing"
)

func TestConfigDataTOML_empty(t *testing.T) {
	b, err := toml.Marshal(ConfigData{})
	if err != nil {
		t.Errorf("failed to marshal")
	}
	if len(b) > 0 {
		t.Errorf("expected no data but got: %s", string(b))
	}
}

func TestConfigDataTOML_all(t *testing.T) {
	cfg := ConfigData{
		URL:     "http://127.0.0.1:8040",
		Port:    "8545",
		RPM:     1000,
		NoLimit: []string{"test", "test2"},
		Allow:   []string{"eth_method", "eth_method2"},
	}
	data := `Allow = [
  "eth_method",
  "eth_method2",
]
NoLimit = [
  "test",
  "test2",
]
Port = "8545"
RPM = 1000
URL = "http://127.0.0.1:8040"
`
	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).ArraysWithOneElementPerLine(true).Encode(cfg)
	if err != nil {
		t.Errorf("failed to marshal: %v", err)
	}
	if have := buf.String(); have != data {
		t.Errorf("failed\n\twant: %s\n\thave: %s", data, have)
	}

	var cfg2 ConfigData
	if err := toml.Unmarshal([]byte(data), &cfg2); err != nil {
		t.Errorf("failed to unmarshal: %v", err)
	}
	if !reflect.DeepEqual(cfg2, cfg) {
		t.Errorf("failed\n\twant: %#v\n\thave: %#v", cfg, cfg2)
	}
}

func TestConfigDataTOML_omitempty(t *testing.T) {
	cfg := ConfigData{
		URL:  "ws://127.0.0.1:8041",
		Port: "8546",
	}
	data := `Port = "8546"
URL = "ws://127.0.0.1:8041"
`
	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).ArraysWithOneElementPerLine(true).Encode(cfg)
	if err != nil {
		t.Errorf("failed to marshal: %v", err)
	}
	if have := buf.String(); have != data {
		t.Errorf("failed\n\twant: %s\n\thave: %s", data, have)
	}

	var cfg2 ConfigData
	if err := toml.Unmarshal([]byte(data), &cfg2); err != nil {
		t.Errorf("failed to unmarshal: %v", err)
	}
	if !reflect.DeepEqual(cfg2, cfg) {
		t.Errorf("failed\n\twant: %#v\n\thave: %#v", cfg, cfg2)
	}
}
