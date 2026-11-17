package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Bucket              string          `json:"bucket"`
	Prefix              string          `json:"prefix"`
	Region              string          `json:"region"`
	Endpoint            string          `json:"endpoint"`
	PathStyle           bool            `json:"path_style"`
	AllowLocalHTTP      bool            `json:"allow_local_http"`
	Policy              retainer.Policy `json:"policy"`
	SuccessfulUploadKey string          `json:"successful_upload_key"`
	CAFile              string          `json:"ca_file"`
	AccessKeyFile       string          `json:"access_key_file"`
	SecretKeyFile       string          `json:"secret_key_file"`
	SessionTokenFile    string          `json:"session_token_file"`
	Timeout             string          `json:"timeout"`
	Format              string          `json:"format"`
}

func Load(path string, env []string) (Config, error) {
	c := Config{Timeout: "5m", Format: "json", Policy: retainer.Policy{Minimum: 1}}
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return c, fmt.Errorf("cannot open config")
		}
		defer f.Close()
		body, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if readErr != nil || len(body) > 1<<20 {
			return c, fmt.Errorf("config must be readable and at most 1 MiB")
		}
		d := json.NewDecoder(bytes.NewReader(body))
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil {
			return c, fmt.Errorf("invalid config JSON or unknown field")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return c, fmt.Errorf("config must contain exactly one JSON object")
		}
	}
	setters := map[string]func(string) error{}
	for k, p := range map[string]*string{"BUCKET": &c.Bucket, "PREFIX": &c.Prefix, "REGION": &c.Region, "ENDPOINT": &c.Endpoint, "SUCCESSFUL_UPLOAD_KEY": &c.SuccessfulUploadKey, "CA_FILE": &c.CAFile, "ACCESS_KEY_FILE": &c.AccessKeyFile, "SECRET_KEY_FILE": &c.SecretKeyFile, "SESSION_TOKEN_FILE": &c.SessionTokenFile, "TIMEOUT": &c.Timeout, "FORMAT": &c.Format} {
		setters[k] = func(v string) error { *p = v; return nil }
	}
	for k, p := range map[string]*bool{"PATH_STYLE": &c.PathStyle, "ALLOW_LOCAL_HTTP": &c.AllowLocalHTTP} {
		setters[k] = func(v string) error { b, e := strconv.ParseBool(v); *p = b; return e }
	}
	for k, p := range map[string]*int{"HOURLY": &c.Policy.Hourly, "DAILY": &c.Policy.Daily, "WEEKLY": &c.Policy.Weekly, "MONTHLY": &c.Policy.Monthly, "MINIMUM": &c.Policy.Minimum} {
		setters[k] = func(v string) error { n, e := strconv.Atoi(v); *p = n; return e }
	}
	for _, pair := range env {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || !strings.HasPrefix(k, "RSR_") {
			continue
		}
		set, ok := setters[strings.TrimPrefix(k, "RSR_")]
		if !ok {
			return c, fmt.Errorf("unknown RSR configuration field")
		}
		if set(v) != nil {
			return c, fmt.Errorf("invalid RSR configuration value")
		}
	}
	return c, nil
}
func (c *Config) Validate() error {
	if c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\ \t\r\n") {
		return fmt.Errorf("bucket is required and must be a bucket name")
	}
	if c.Region == "" || strings.ContainsAny(c.Region, "/ \t\r\n") {
		return fmt.Errorf("signing region is required")
	}
	p, e := retainer.NormalizePrefix(c.Prefix)
	if e != nil {
		return e
	}
	c.Prefix = p
	if e = c.Policy.Validate(); e != nil {
		return e
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.HasSuffix(u.Host, ":") || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
			return fmt.Errorf("endpoint must be an origin URL without credentials, path or query")
		}
		local := u.Hostname() == "localhost"
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			local = ip.IsLoopback()
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && c.AllowLocalHTTP && local) {
			return fmt.Errorf("endpoint requires HTTPS; explicit local HTTP accepts only loopback endpoints")
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("invalid endpoint port")
			}
		}
	}
	if (c.AccessKeyFile == "") != (c.SecretKeyFile == "") || (c.SessionTokenFile != "" && c.AccessKeyFile == "") {
		return fmt.Errorf("mounted credentials require both access and secret key files")
	}
	d, e := time.ParseDuration(c.Timeout)
	if e != nil || d <= 0 || d > 24*time.Hour {
		return fmt.Errorf("timeout must be positive and at most 24h")
	}
	if c.Format != "json" && c.Format != "text" {
		return fmt.Errorf("format must be json or text")
	}
	return nil
}
