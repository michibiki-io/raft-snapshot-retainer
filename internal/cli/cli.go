package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/michibiki-io/raft-snapshot-retainer/internal/config"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/storage"
)

type Factory func(context.Context, config.Config) (retainer.Store, error)

func DefaultFactory(ctx context.Context, c config.Config) (retainer.Store, error) {
	return storage.New(ctx, c)
}

// Exit codes: 0 plan/completion, 1 preflight/I/O/partial failure, 2 invalid CLI/config.
func Main(ctx context.Context, args, env []string, out, errOut io.Writer, version string, now func() time.Time, factory Factory) int {
	f := flag.NewFlagSet("raft-snapshot-retainer", flag.ContinueOnError)
	f.SetOutput(errOut)
	path := f.String("config", "", "strict JSON configuration file (RSR_* environment overrides)")
	apply := f.Bool("apply", false, "delete eligible objects after all preflight checks (default: dry-run)")
	pin := f.String("successful-upload-key", "", "exact completed object key to verify and keep (required for apply)")
	ver := f.Bool("version", false, "print version")
	f.Usage = func() {
		fmt.Fprintln(errOut, "Usage: raft-snapshot-retainer [--config FILE] [--apply] [--successful-upload-key KEY]\nConfiguration: RSR_BUCKET, RSR_PREFIX, RSR_REGION, explicit RSR_HOURLY/DAILY/WEEKLY/MONTHLY counts; see README.md.")
		f.PrintDefaults()
	}
	if e := f.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return 0
		}
		fmt.Fprintln(errOut, "invalid command line")
		return 2
	}
	if f.NArg() != 0 {
		fmt.Fprintln(errOut, "unexpected positional arguments")
		return 2
	}
	if *ver {
		fmt.Fprintln(out, version)
		return 0
	}
	c, e := config.Load(*path, env)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	f.Visit(func(fl *flag.Flag) {
		if fl.Name == "successful-upload-key" {
			c.SuccessfulUploadKey = *pin
		}
	})
	if e = c.Validate(); e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	if *apply && c.SuccessfulUploadKey == "" {
		fmt.Fprintln(errOut, "apply requires successful-upload-key")
		return 2
	}
	duration, _ := time.ParseDuration(c.Timeout)
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	store, e := factory(ctx, c)
	if e != nil {
		fmt.Fprintln(errOut, "storage_initialization_failed")
		return 1
	}
	r := retainer.Run(ctx, store, c.Bucket, c.Prefix, c.Policy, c.SuccessfulUploadKey, *apply, now())
	if c.Format == "json" {
		e = json.NewEncoder(out).Encode(r)
	} else {
		_, e = fmt.Fprintf(out, "%s %s s3://%s/%s protection=%s keep=%d delete=%d ignore=%d invalid=%d deleted=%d\n", r.Mode, r.Status, r.Bucket, r.Prefix, r.Protection, r.Counts["keep"], r.Counts["delete"], r.Counts["ignore"], r.Counts["invalid"], len(r.Deleted))
		for _, d := range r.Plan.Decisions {
			if e == nil {
				_, e = fmt.Fprintf(out, "%s %q %v\n", d.Action, d.Key, d.Reasons)
			}
		}
		for _, problem := range r.Errors {
			if e == nil {
				_, e = fmt.Fprintf(out, "error %q\n", problem)
			}
		}
	}
	if e != nil {
		fmt.Fprintln(errOut, "output_failed")
		return 1
	}
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}
