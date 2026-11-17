// Package retainer implements deterministic, storage-independent GFS selection.
package retainer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Policy struct {
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Minimum int `json:"minimum"`
}

func (p Policy) Validate() error {
	if p.Hourly < 0 || p.Daily < 0 || p.Weekly < 0 || p.Monthly < 0 || p.Minimum < 1 {
		return fmt.Errorf("counts must be nonnegative and minimum must be positive")
	}
	if p.Hourly == 0 && p.Daily == 0 && p.Weekly == 0 && p.Monthly == 0 {
		return fmt.Errorf("at least one GFS count must be positive")
	}
	return nil
}
func NormalizePrefix(s string) (string, error) {
	s = strings.TrimSuffix(s, "/")
	if s == "" || strings.HasPrefix(s, "/") || strings.Contains(s, "//") || strings.TrimSpace(s) != s {
		return "", fmt.Errorf("prefix must be a non-root relative directory")
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("prefix must not contain empty or dot segments")
		}
	}
	return s + "/", nil
}

type Object struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}
type Decision struct {
	Object
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Action    string     `json:"action"`
	Reasons   []string   `json:"reasons"`
}
type Plan struct {
	Decisions []Decision `json:"decisions"`
	Problems  []string   `json:"problems"`
}

var nameRE = regexp.MustCompile(`^bao_[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{4}\.snapshot$`)

func Parse(key, prefix string) (time.Time, string) {
	if !strings.HasPrefix(key, prefix) {
		return time.Time{}, "outside_prefix"
	}
	name := strings.TrimPrefix(key, prefix)
	if name == "" || strings.HasSuffix(name, "/") {
		return time.Time{}, "directory_marker"
	}
	if strings.Contains(name, "/") {
		return time.Time{}, "nested_object"
	}
	if !nameRE.MatchString(name) {
		return time.Time{}, "unrecognized_name"
	}
	t, e := time.Parse("bao_2006-01-02-1504.snapshot", name)
	if e != nil || t.Year() < 1 {
		return time.Time{}, "invalid_timestamp"
	}
	return t, ""
}

// Select does not mutate its inputs. Timestamp descending, then key descending,
// chooses representatives; the returned plan is oldest first, then key ascending.
func Select(objects []Object, prefix string, p Policy, pin string, now time.Time) (Plan, error) {
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	n, err := NormalizePrefix(prefix)
	if err != nil {
		return Plan{}, err
	}
	out := Plan{Decisions: []Decision{}, Problems: []string{}}
	seen := map[string]bool{}
	for _, o := range objects {
		if seen[o.Key] {
			out.Problems = append(out.Problems, "duplicate_object_key")
			continue
		}
		seen[o.Key] = true
		t, reason := Parse(o.Key, n)
		d := Decision{Object: o, Action: "ignore", Reasons: []string{reason}}
		if reason == "" {
			d.Timestamp = &t
			d.Action = "delete"
			d.Reasons = []string{"outside_retention"}
			if o.Size <= 0 {
				d.Action = "invalid"
				d.Reasons = []string{"nonpositive_size"}
				out.Problems = append(out.Problems, "nonpositive_size")
			}
			if t.After(now.UTC()) {
				d.Action = "invalid"
				d.Reasons = []string{"future_timestamp"}
				out.Problems = append(out.Problems, "future_timestamp")
			}
		}
		out.Decisions = append(out.Decisions, d)
	}
	sort.Slice(out.Decisions, func(i, j int) bool {
		a, b := out.Decisions[i], out.Decisions[j]
		if a.Timestamp == nil || b.Timestamp == nil {
			if (a.Timestamp == nil) != (b.Timestamp == nil) {
				return a.Timestamp == nil
			}
			return a.Key < b.Key
		}
		if a.Timestamp.Equal(*b.Timestamp) {
			return a.Key < b.Key
		}
		return a.Timestamp.Before(*b.Timestamp)
	})
	periods := []map[string]bool{{}, {}, {}, {}}
	limits := []int{p.Hourly, p.Daily, p.Weekly, p.Monthly}
	classes := []string{"hourly", "daily", "weekly", "monthly"}
	rank := 0
	for i := len(out.Decisions) - 1; i >= 0; i-- {
		d := &out.Decisions[i]
		if d.Action != "delete" {
			continue
		}
		rank++
		t := *d.Timestamp
		y, w := t.ISOWeek()
		keys := []string{t.Format("2006-01-02T15"), t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", y, w), t.Format("2006-01")}
		reasons := []string{}
		for c, k := range keys {
			if !periods[c][k] && len(periods[c]) < limits[c] {
				periods[c][k] = true
				reasons = append(reasons, classes[c])
			}
		}
		if rank <= p.Minimum {
			reasons = append(reasons, "minimum")
		}
		if d.Key == pin {
			reasons = append(reasons, "successful_upload")
		}
		if len(reasons) > 0 {
			d.Action = "keep"
			d.Reasons = reasons
		}
	}
	return out, nil
}
