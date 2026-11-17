package config

import (
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() Config {
	return Config{Bucket: "test-bucket", Region: "test-1", Prefix: "openbao", Timeout: "1m", Format: "json", Policy: retainer.Policy{Daily: 7, Minimum: 1}}
}
func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"bucket", func(c *Config) { c.Bucket = "" }}, {"prefix", func(c *Config) { c.Prefix = "/" }}, {"region", func(c *Config) { c.Region = "" }},
		{"count", func(c *Config) { c.Policy.Daily = -1 }}, {"timeout", func(c *Config) { c.Timeout = "today" }}, {"format", func(c *Config) { c.Format = "xml" }},
		{"http", func(c *Config) { c.Endpoint = "http://example.com"; c.AllowLocalHTTP = true }}, {"tls credentials", func(c *Config) { c.Endpoint = "https://user:password@host" }},
		{"path", func(c *Config) { c.Endpoint = "https://host/bucket" }}, {"port", func(c *Config) { c.Endpoint = "https://host:99999" }},
		{"query", func(c *Config) { c.Endpoint = "https://host?secret=x" }}, {"partial credentials", func(c *Config) { c.AccessKeyFile = "file" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.edit(&c)
			if c.Validate() == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	for _, endpoint := range []string{"", "https://s3.example.test", "https://localhost:9000", "http://127.0.0.1:9000", "http://[::1]:9000"} {
		c := valid()
		c.Endpoint = endpoint
		c.AllowLocalHTTP = true
		if e := c.Validate(); e != nil {
			t.Fatal(endpoint, e)
		}
	}
}
func TestStrictLoad(t *testing.T) {
	for _, content := range []string{`{"unknown":1}`, `{"policy":{"unknown":1}}`, `{} {}`, `{"bucket":`, `{"policy":{"daily":"7"}}`, "{}" + strings.Repeat(" ", 1<<20) + `{"unknown":1}`} {
		p := filepath.Join(t.TempDir(), "config.json")
		if e := os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p, nil); e == nil {
			t.Fatal(content)
		}
	}
	for _, env := range [][]string{{"RSR_UNKNOWN=secret"}, {"RSR_DAILY=no"}, {"RSR_PATH_STYLE=maybe"}} {
		if _, e := Load("", env); e == nil {
			t.Fatal(env)
		}
	}
	c, e := Load("", []string{"RSR_BUCKET=test", "RSR_REGION=local", "RSR_PREFIX=bao", "RSR_DAILY=7", "RSR_PATH_STYLE=true"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Validate(); e != nil {
		t.Fatal(e)
	}
	if c.Policy.Daily != 7 || !c.PathStyle {
		t.Fatal(c)
	}
}

func TestFileEnvironmentPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if e := os.WriteFile(path, []byte(`{"bucket":"file-bucket","prefix":"file-prefix","region":"file-region","policy":{"daily":7,"minimum":2},"successful_upload_key":"file-key"}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path, []string{"RSR_BUCKET=env-bucket", "RSR_DAILY=3", "RSR_SUCCESSFUL_UPLOAD_KEY=env-key"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Validate(); e != nil {
		t.Fatal(e)
	}
	if c.Bucket != "env-bucket" || c.Prefix != "file-prefix/" || c.Region != "file-region" || c.Policy.Daily != 3 || c.Policy.Minimum != 2 || c.SuccessfulUploadKey != "env-key" {
		t.Fatalf("incorrect precedence: %+v", c)
	}
}
