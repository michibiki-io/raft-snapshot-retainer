package storage

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/config"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
)

func testConfig(endpoint string) config.Config {
	return config.Config{Bucket: "test-bucket", Prefix: "openbao/", Region: "us-east-1", Endpoint: endpoint, AllowLocalHTTP: true, PathStyle: true, Timeout: "1m", Format: "json", Policy: retainer.Policy{Daily: 1, Minimum: 1}}
}
func envCreds(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test-only-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-only-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
}
func TestPaginationAndFailClosed(t *testing.T) {
	for _, mode := range []string{"ok", "list_error", "bad_token", "versioned", "suspended", "unknown_version", "locked", "lock_denied", "lock_not_implemented", "empty_lock", "head_missing", "delete_error"} {
		t.Run(mode, func(t *testing.T) {
			envCreds(t)
			writes := []string{}
			pages := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "GET" && q.Get("list-type") == "2":
					pages++
					if q.Get("prefix") != "openbao/" {
						t.Error("wrong prefix")
					}
					if pages > 1 && mode == "list_error" {
						w.WriteHeader(403)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
						return
					}
					if pages == 1 {
						token := "next"
						if mode == "bad_token" {
							token = ""
						}
						fmt.Fprintf(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>%s</NextContinuationToken><Contents><Key>openbao/bao_2025-01-01-0000.snapshot</Key><Size>10</Size></Contents></ListBucketResult>`, token)
					} else {
						if q.Get("continuation-token") != "next" {
							t.Error("missing token")
						}
						fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>openbao/bao_2025-01-02-0000.snapshot</Key><Size>10</Size></Contents></ListBucketResult>`)
					}
				case r.Method == "GET" && q.Has("versioning"):
					status := ""
					switch mode {
					case "versioned":
						status = "Enabled"
					case "suspended":
						status = "Suspended"
					case "unknown_version":
						status = "FutureValue"
					}
					fmt.Fprintf(w, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, status)
				case r.Method == "GET" && q.Has("object-lock"):
					switch mode {
					case "locked":
						fmt.Fprint(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
					case "empty_lock":
						fmt.Fprint(w, `<ObjectLockConfiguration/>`)
					case "lock_denied":
						w.WriteHeader(403)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
					case "lock_not_implemented":
						w.WriteHeader(501)
						fmt.Fprint(w, `<Error><Code>NotImplemented</Code></Error>`)
					default:
						w.WriteHeader(404)
						fmt.Fprint(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
					}
				case r.Method == "HEAD":
					if mode == "head_missing" {
						w.WriteHeader(404)
					} else {
						w.Header().Set("Content-Length", "10")
					}
				case r.Method == "DELETE":
					writes = append(writes, r.URL.Path)
					if mode == "delete_error" {
						w.WriteHeader(403)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
					} else {
						w.WriteHeader(204)
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			}))
			defer srv.Close()
			store, e := New(context.Background(), testConfig(srv.URL))
			if e != nil {
				t.Fatal(e)
			}
			result := retainer.Run(context.Background(), store, "test-bucket", "openbao/", retainer.Policy{Daily: 1, Minimum: 1}, "openbao/bao_2025-01-02-0000.snapshot", true, time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC))
			if mode == "ok" {
				if result.Status != "completed" || len(writes) != 1 || pages != 2 {
					t.Fatalf("%+v %v", result, writes)
				}
			} else {
				if len(result.Errors) == 0 {
					t.Fatalf("accepted %s: %+v", mode, result)
				}
				if mode != "delete_error" && len(writes) != 0 {
					t.Fatal("write before complete validation")
				}
			}
		})
	}
}
func TestCredentialSelection(t *testing.T) {
	envCreds(t)
	c := testConfig("http://127.0.0.1:9000")
	for _, pair := range [][2]string{{"", "test-only-secret"}, {"test-only-key", ""}, {"", ""}} {
		t.Setenv("AWS_ACCESS_KEY_ID", pair[0])
		t.Setenv("AWS_SECRET_ACCESS_KEY", pair[1])
		t.Setenv("AWS_SESSION_TOKEN", "test-only-token")
		if _, e := New(context.Background(), c); e == nil {
			t.Fatal("accepted partial env credentials")
		}
	}
	dir := t.TempDir()
	c.AccessKeyFile = filepath.Join(dir, "access")
	c.SecretKeyFile = filepath.Join(dir, "secret")
	for p, v := range map[string]string{c.AccessKeyFile: "file-only-key\n", c.SecretKeyFile: "file-only-secret\n"} {
		if e := os.WriteFile(p, []byte(v), 0600); e != nil {
			t.Fatal(e)
		}
	}
	// Explicit mounted credentials win even over invalid environment credentials.
	if _, e := New(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.SecretKeyFile, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := New(context.Background(), c); e == nil {
		t.Fatal("empty file fell through")
	}
}
func TestTLSVerificationAndCustomCA(t *testing.T) {
	envCreds(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<ListBucketResult/>`) }))
	defer srv.Close()
	c := testConfig(srv.URL)
	s, e := New(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e = s.List(ctx); e == nil {
		t.Fatal("accepted untrusted TLS")
	}
	c.CAFile = filepath.Join(t.TempDir(), "trusted-ca")
	if e = os.WriteFile(c.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	s, e = New(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(context.Background()); e != nil {
		t.Fatal("custom CA did not establish trust", e)
	}
	// A malformed custom bundle must fail rather than disable TLS verification.
	c.CAFile = filepath.Join(t.TempDir(), "bad-ca")
	if e = os.WriteFile(c.CAFile, []byte("not a certificate"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = New(context.Background(), c); e == nil {
		t.Fatal("accepted bad CA")
	}
}
func TestNoWritesDryRun(t *testing.T) {
	envCreds(t)
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			writes++
		}
		if strings.Contains(r.URL.RawQuery, "list-type") {
			fmt.Fprint(w, `<ListBucketResult/>`)
		} else {
			w.WriteHeader(403)
		}
	}))
	defer srv.Close()
	s, e := New(context.Background(), testConfig(srv.URL))
	if e != nil {
		t.Fatal(e)
	}
	r := retainer.Run(context.Background(), s, "test-bucket", "openbao/", retainer.Policy{Daily: 1, Minimum: 1}, "", false, time.Now())
	if r.Status != "planned" || writes != 0 {
		t.Fatal(r, writes)
	}
}

// Compile-time checks keep SDK interface signatures in sync.
var _ Client = (*s3.Client)(nil)

func TestDeleteResponseHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		wantErr bool
	}{
		{"no version", nil, false},
		{"null version", map[string]string{"x-amz-version-id": "null"}, false},
		{"empty version", map[string]string{"x-amz-version-id": ""}, false},
		{"real version", map[string]string{"x-amz-version-id": "version-123"}, true},
		{"delete marker", map[string]string{"x-amz-delete-marker": "true", "x-amz-version-id": "null"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envCreds(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "DELETE" {
					t.Errorf("unexpected method %s", r.Method)
				}
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(204)
			}))
			defer srv.Close()
			store, e := New(context.Background(), testConfig(srv.URL))
			if e != nil {
				t.Fatal(e)
			}
			if e = store.Delete(context.Background(), "openbao/bao_2025-01-01-0000.snapshot"); (e != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", e, tc.wantErr)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
