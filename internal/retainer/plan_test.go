package retainer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return t
}
func obj(stamp string) Object { return Object{Key: "openbao/bao_" + stamp + ".snapshot", Size: 10} }
func TestCalendarSelection(t *testing.T) {
	objects := []Object{obj("2024-12-29-2359"), obj("2024-12-30-0000"), obj("2025-01-01-0000"), obj("2025-01-01-0030"), obj("2025-01-01-0100"), obj("2025-03-04-0100")}
	cases := []struct {
		name string
		p    Policy
		want []int
	}{
		{"hourly", Policy{Hourly: 3, Minimum: 1}, []int{3, 4, 5}},
		{"daily", Policy{Daily: 3, Minimum: 1}, []int{1, 4, 5}},
		{"ISO week and year", Policy{Weekly: 3, Minimum: 1}, []int{0, 4, 5}},
		{"calendar months and gaps", Policy{Monthly: 2, Minimum: 1}, []int{4, 5}},
		{"union", Policy{Hourly: 1, Daily: 2, Weekly: 3, Monthly: 3, Minimum: 1}, []int{0, 1, 4, 5}},
		{"minimum", Policy{Daily: 1, Minimum: 4}, []int{2, 3, 4, 5}},
		{"floor exceeds inventory", Policy{Daily: 1, Minimum: 20}, []int{0, 1, 2, 3, 4, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, e := Select(objects, "openbao", tc.p, "", instant("2025-03-05T00:00:00Z"))
			if e != nil {
				t.Fatal(e)
			}
			var got []int
			for i, d := range p.Decisions {
				if d.Action == "keep" {
					got = append(got, i)
				}
				if len(d.Reasons) == 0 {
					t.Fatal("missing reason")
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			reversed := append([]Object{}, objects...)
			for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
				reversed[i], reversed[j] = reversed[j], reversed[i]
			}
			again, _ := Select(reversed, "openbao/", tc.p, "", instant("2025-03-05T00:00:00Z"))
			if !reflect.DeepEqual(p, again) {
				t.Fatal("input ordering affected output")
			}
		})
	}
}
func TestNamesAndSafety(t *testing.T) {
	objects := []Object{obj("2025-01-01-0000"), obj("2025-01-02-0000"), {Key: "openbao-other/bao_2025-01-01-0000.snapshot", Size: 2}, {Key: "openbao/cluster-b/bao_2025-01-01-0000.snapshot"}, {Key: "openbao/"}, {Key: "openbao/bao_2025-02-30-0000.snapshot"}, {Key: "openbao/notes"}}
	p, e := Select(objects, "openbao", Policy{Daily: 1, Minimum: 1}, objects[0].Key, instant("2025-01-03T00:00:00Z"))
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range p.Decisions {
		if d.Timestamp != nil {
			if d.Action != "keep" {
				t.Fatal(d)
			}
		} else if d.Action != "ignore" {
			t.Fatal(d)
		}
	}
	bad := []Object{{Key: obj("2025-01-01-0000").Key}, {Key: obj("2026-01-01-0000").Key, Size: 5}}
	p, _ = Select(bad, "openbao", Policy{Daily: 1, Minimum: 1}, "", instant("2025-01-03T00:00:00Z"))
	if len(p.Problems) != 2 {
		t.Fatal(p)
	}
	p, _ = Select([]Object{objects[0], objects[0]}, "openbao", Policy{Daily: 1, Minimum: 1}, "", instant("2025-01-03T00:00:00Z"))
	if len(p.Problems) != 1 {
		t.Fatal(p)
	}
	for _, s := range []string{"", "/", "//", "/a", "a//b", "a/../b", "a/./b", " a"} {
		if _, e := NormalizePrefix(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, p := range []Policy{{}, {Daily: -1, Minimum: 1}, {Daily: 1}, {Minimum: 1}} {
		if p.Validate() == nil {
			t.Fatal(p)
		}
	}
}

type fakeStore struct {
	objects                         []Object
	deleted                         []string
	listErr, headErr, protectionErr error
	state                           string
	head                            *Object
	failAt                          int
	cancel                          context.CancelFunc
}

func (s *fakeStore) List(context.Context) ([]Object, error) { return s.objects, s.listErr }
func (s *fakeStore) Head(_ context.Context, key string) (Object, error) {
	if s.head != nil {
		return *s.head, s.headErr
	}
	for _, o := range s.objects {
		if o.Key == key {
			return o, s.headErr
		}
	}
	return Object{}, errors.New("missing")
}
func (s *fakeStore) Protection(context.Context) (string, error) { return s.state, s.protectionErr }
func (s *fakeStore) Delete(_ context.Context, key string) error {
	if s.failAt > 0 && len(s.deleted)+1 == s.failAt {
		return errors.New("secret must never escape")
	}
	s.deleted = append(s.deleted, key)
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
func TestPreflightAndExecution(t *testing.T) {
	base := []Object{obj("2025-01-01-0000"), obj("2025-01-02-0000"), obj("2025-01-03-0000"), obj("2025-01-04-0000")}
	pin := base[3].Key
	cases := []struct {
		name  string
		apply bool
		pin   string
		edit  func(*fakeStore)
		want  string
		n     int
	}{
		{"dry run", false, "", func(s *fakeStore) {}, "planned", 0},
		{"apply", true, pin, func(s *fakeStore) {}, "completed", 3},
		{"missing signal", true, "", func(s *fakeStore) {}, "failed", 0},
		{"absent signal", true, obj("2025-01-05-0000").Key, func(s *fakeStore) {}, "failed", 0},
		{"nested signal", true, "openbao/sub/bao_2025-01-04-0000.snapshot", func(s *fakeStore) {}, "failed", 0},
		{"list failure", true, pin, func(s *fakeStore) { s.listErr = errors.New("secret") }, "failed", 0},
		{"head failure", true, pin, func(s *fakeStore) { s.headErr = errors.New("secret") }, "failed", 0},
		{"head mismatch", true, pin, func(s *fakeStore) { s.head = &Object{Key: pin, Size: 11} }, "failed", 0},
		{"empty upload", true, pin, func(s *fakeStore) { s.head = &Object{Key: pin} }, "failed", 0},
		{"recognized empty", true, pin, func(s *fakeStore) { s.objects[0].Size = 0 }, "failed", 0},
		{"future", true, pin, func(s *fakeStore) { s.objects = append(s.objects, obj("2026-01-01-0000")) }, "failed", 0},
		{"locked", true, pin, func(s *fakeStore) { s.state = "object_lock_configured" }, "unsupported", 0},
		{"versioned", true, pin, func(s *fakeStore) { s.state = "versioned_or_mfa_delete" }, "unsupported", 0},
		{"unknown", true, pin, func(s *fakeStore) { s.protectionErr = errors.New("secret") }, "unsupported", 0},
		{"dry unsupported notice", false, "", func(s *fakeStore) { s.protectionErr = errors.New("secret") }, "planned", 0},
		{"partial failure", true, pin, func(s *fakeStore) { s.failAt = 2 }, "failed", 1},
		{"empty store", false, "", func(s *fakeStore) { s.objects = nil }, "planned", 0},
		{"too few", true, pin, func(s *fakeStore) { s.objects = s.objects[3:] }, "completed", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeStore{objects: append([]Object{}, base...), state: "unversioned_unlocked"}
			tc.edit(s)
			r := Run(context.Background(), s, "bucket", "openbao", Policy{Daily: 1, Minimum: 1}, tc.pin, tc.apply, instant("2025-01-05T00:00:00Z"))
			if r.Status != tc.want || len(s.deleted) != tc.n {
				t.Fatalf("result=%+v deleted=%v", r, s.deleted)
			}
			for i, k := range s.deleted {
				if k != base[i].Key {
					t.Fatal("not oldest first")
				}
			}
		})
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeStore{objects: []Object{obj("2025-01-01-0000"), obj("2025-01-02-0000"), obj("2025-01-03-0000")}, state: "unversioned_unlocked", cancel: cancel}
	r := Run(ctx, s, "bucket", "openbao", Policy{Daily: 1, Minimum: 1}, s.objects[2].Key, true, instant("2025-01-04T00:00:00Z"))
	if r.Status != "failed" || len(r.Deleted) != 1 {
		t.Fatal(r)
	}
}
