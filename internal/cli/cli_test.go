package cli

import (
	"bytes"
	"context"
	"errors"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/config"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
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
