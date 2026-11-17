package retainer

import (
	"context"
	"fmt"
	"time"
)

type Store interface {
	List(context.Context) ([]Object, error)
	Head(context.Context, string) (Object, error)
	Protection(context.Context) (string, error)
	Delete(context.Context, string) error
}
type Result struct {
	SchemaVersion int            `json:"schema_version"`
	Mode          string         `json:"mode"`
	Status        string         `json:"status"`
	Bucket        string         `json:"bucket"`
	Prefix        string         `json:"prefix"`
	Policy        Policy         `json:"policy"`
	Protection    string         `json:"protection"`
	Plan          Plan           `json:"plan"`
	Deleted       []string       `json:"deleted"`
	Errors        []string       `json:"errors"`
	Counts        map[string]int `json:"counts"`
}

// Run completes the read-only phase before any write. Store errors are deliberately
// replaced with stable codes so SDK/HTTP errors cannot leak secrets into output.
func Run(ctx context.Context, s Store, bucket, prefix string, p Policy, pin string, apply bool, now time.Time) Result {
	r := Result{SchemaVersion: 1, Mode: "dry-run", Status: "failed", Bucket: bucket, Prefix: prefix, Policy: p, Protection: "unknown", Deleted: []string{}, Errors: []string{}, Counts: map[string]int{}}
	if apply {
		r.Mode = "apply"
	}
	fail := func(code string) Result { r.Errors = append(r.Errors, code); return r }
	n, err := NormalizePrefix(prefix)
	if err != nil {
		return fail("invalid_prefix")
	}
	r.Prefix = n
	if p.Validate() != nil {
		return fail("invalid_policy")
	}
	if apply && pin == "" {
		return fail("successful_upload_key_required")
	}
	objects, err := s.List(ctx)
	if err != nil {
		return fail("list_failed")
	}
	r.Plan, err = Select(objects, n, p, pin, now)
	if err != nil {
		return fail("plan_failed")
	}
	for _, d := range r.Plan.Decisions {
		r.Counts[d.Action]++
	}
	state, protectionErr := s.Protection(ctx)
	if state != "" {
		r.Protection = state
	}
	if protectionErr != nil && state == "" {
		r.Protection = "unknown"
	}
	if pin != "" {
		t, reason := Parse(pin, n)
		if reason != "" || t.After(now.UTC()) {
			return fail("invalid_successful_upload_key")
		}
		found := false
		var listed Object
		for _, d := range r.Plan.Decisions {
			if d.Key == pin && d.Action == "keep" {
				found = true
				listed = d.Object
			}
		}
		if !found {
			return fail("successful_upload_not_eligible")
		}
		h, e := s.Head(ctx, pin)
		if e != nil || h.Key != pin || h.Size <= 0 || h.Size != listed.Size {
			return fail("successful_upload_check_failed")
		}
	}
	if len(r.Plan.Problems) > 0 {
		return fail("inconsistent_snapshots")
	}
	if apply && (protectionErr != nil || state != "unversioned_unlocked") {
		r.Status = "unsupported"
		return fail("unsupported_or_unknown_bucket_protection")
	}
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	if !apply {
		r.Status = "planned"
		return r
	}
	for _, d := range r.Plan.Decisions {
		if d.Action != "delete" {
			continue
		}
		if ctx.Err() != nil {
			return fail("cancelled")
		}
		if e := s.Delete(ctx, d.Key); e != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("delete_failed:%s", d.Key))
			return r
		}
		r.Deleted = append(r.Deleted, d.Key)
	}
	r.Status = "completed"
	return r
}
