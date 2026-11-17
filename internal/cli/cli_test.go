package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/config"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandContract(t *testing.T) {
	env := []string{"RSR_BUCKET=test", "RSR_REGION=local", "RSR_PREFIX=bao", "RSR_DAILY=7"}
	factory := func(context.Context, config.Config) (retainer.Store, error) {
		return nil, errors.New("credential-super-secret")
	}
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"--help"}, 0}, {[]string{"--version"}, 0}, {[]string{"--unknown"}, 2}, {[]string{"--apply"}, 2}, {nil, 1}, {[]string{"positional"}, 2}} {
		var out, stderr bytes.Buffer
		code := Main(context.Background(), tc.args, env, &out, &stderr, "v0.1.0", time.Now, factory)
		if code != tc.code {
			t.Fatalf("%v: %d %s", tc.args, code, stderr.String())
		}
		if strings.Contains(out.String()+stderr.String(), "super-secret") {
			t.Fatal("leak")
		}
	}
}

type memoryStore struct {
	objects []retainer.Object
	deleted []string
	fail    bool
}

func (s *memoryStore) List(context.Context) ([]retainer.Object, error) { return s.objects, nil }
func (s *memoryStore) Head(_ context.Context, key string) (retainer.Object, error) {
	for _, o := range s.objects {
		if o.Key == key {
			return o, nil
		}
	}
	return retainer.Object{}, errors.New("absent")
}
func (s *memoryStore) Protection(context.Context) (string, error) { return "unversioned_unlocked", nil }
func (s *memoryStore) Delete(_ context.Context, key string) error {
	if s.fail {
		return errors.New("secret-delete-error")
	}
	s.deleted = append(s.deleted, key)
	return nil
}

func TestOutputAndPrecedence(t *testing.T) {
	keys := []string{"openbao/bao_2025-01-01-0000.snapshot", "openbao/bao_2025-01-02-0000.snapshot", "openbao/bao_2025-01-03-0000.snapshot"}
	path := filepath.Join(t.TempDir(), "config.json")
	body := fmt.Sprintf(`{"bucket":"file-bucket","prefix":"openbao","region":"us-east-1","policy":{"daily":7,"minimum":1},"successful_upload_key":%q}`, keys[2])
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, format string
		apply, fail  bool
		wantCode     int
		status       string
	}{
		{"JSON plan", "json", false, false, 0, "planned"}, {"JSON apply", "json", true, false, 0, "completed"},
		{"text plan", "text", false, false, 0, "planned"}, {"text apply", "text", true, false, 0, "completed"},
		{"text deletion failure", "text", true, true, 1, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryStore{fail: tc.fail}
			for _, k := range keys {
				store.objects = append(store.objects, retainer.Object{Key: k, Size: 10})
			}
			var effective config.Config
			factory := func(_ context.Context, c config.Config) (retainer.Store, error) { effective = c; return store, nil }
			args := []string{"--config", path, "--successful-upload-key", keys[0]}
			if tc.apply {
				args = append(args, "--apply")
			}
			env := []string{"RSR_BUCKET=env-bucket", "RSR_DAILY=1", "RSR_SUCCESSFUL_UPLOAD_KEY=" + keys[1], "RSR_FORMAT=" + tc.format}
			var stdout, stderr bytes.Buffer
			clock := func() time.Time { return time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC) }
			code := Main(context.Background(), args, env, &stdout, &stderr, "v0.1.0", clock, factory)
			if code != tc.wantCode {
				t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
			}
			if effective.Bucket != "env-bucket" || effective.Policy.Daily != 1 || effective.SuccessfulUploadKey != keys[0] {
				t.Fatalf("precedence %+v", effective)
			}
			if tc.format == "json" {
				var r retainer.Result
				if e := json.Unmarshal(stdout.Bytes(), &r); e != nil {
					t.Fatal(e)
				}
				if r.SchemaVersion != 1 || r.Status != tc.status || r.Counts["keep"] != 2 || r.Counts["delete"] != 1 || len(r.Plan.Decisions) != 3 {
					t.Fatalf("result %+v", r)
				}
				if tc.apply && (len(r.Deleted) != 1 || r.Deleted[0] != keys[1]) {
					t.Fatal(r.Deleted)
				}
			} else {
				text := stdout.String()
				for _, want := range []string{tc.status, "s3://env-bucket/openbao/", "keep=2 delete=1", `keep "` + keys[0] + `"`, `delete "` + keys[1] + `"`} {
					if !strings.Contains(text, want) {
						t.Errorf("missing %q in %s", want, text)
					}
				}
				if tc.fail && !strings.Contains(text, `error "delete_failed:`+keys[1]+`"`) {
					t.Fatal(text)
				}
			}
			if strings.Contains(stdout.String()+stderr.String(), "secret-delete-error") {
				t.Fatal("raw error leaked")
			}
			if !tc.apply && len(store.deleted) > 0 {
				t.Fatal("dry run wrote")
			}
		})
	}
}
