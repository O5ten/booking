package booking

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func messages(errs []Error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Message
	}
	return strings.Join(parts, " | ")
}

func TestValidateAcceptsAGoodRequest(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")

	from, to, errs := Validate(context.Background(), st, Request{
		Resource: bike(),
		Start:    at(loc, "2026-05-02 10:00"),
		End:      at(loc, "2026-05-02 14:00"),
		Name:     "Anna Andersson",
		Email:    "anna@example.se",
	}, now, loc)

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %s", messages(errs))
	}
	// The blocked window is the booking widened by the 15 minute buffer.
	if got := from.In(loc).Format("15:04"); got != "09:45" {
		t.Errorf("block starts %s, want 09:45", got)
	}
	if got := to.In(loc).Format("15:04"); got != "14:15" {
		t.Errorf("block ends %s, want 14:15", got)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")
	base := Request{
		Resource: bike(),
		Start:    at(loc, "2026-05-02 10:00"),
		End:      at(loc, "2026-05-02 14:00"),
		Name:     "Anna",
		Email:    "anna@example.se",
	}

	cases := []struct {
		name    string
		mutate  func(*Request)
		wantSub string
	}{
		{"no name", func(r *Request) { r.Name = "  " }, "namn"},
		{"bad e-mail", func(r *Request) { r.Email = "anna(at)example" }, "e-postadress"},
		{"unlisted duration", func(r *Request) { r.End = r.Start.Add(3 * time.Hour) }, "tillåtna längderna"},
		{"before opening", func(r *Request) {
			r.Start = at(loc, "2026-05-02 04:00")
			r.End = r.Start.Add(4 * time.Hour)
		}, "kan bokas mellan"},
		{"after closing", func(r *Request) {
			r.Start = at(loc, "2026-05-02 20:00")
			r.End = r.Start.Add(4 * time.Hour)
		}, "kan bokas mellan"},
		{"off the slot grid", func(r *Request) {
			r.Start = at(loc, "2026-05-02 10:10")
			r.End = r.Start.Add(4 * time.Hour)
		}, "30-minutersgräns"},
		{"in the past", func(r *Request) {
			r.Start = at(loc, "2026-04-20 10:00")
			r.End = r.Start.Add(4 * time.Hour)
		}, "passerat"},
		{"beyond the horizon", func(r *Request) {
			r.Start = at(loc, "2026-09-02 10:00")
			r.End = r.Start.Add(4 * time.Hour)
		}, "dagar i förväg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			_, _, errs := Validate(context.Background(), st, req, now, loc)
			if len(errs) == 0 {
				t.Fatalf("expected an error mentioning %q, got none", tc.wantSub)
			}
			if !strings.Contains(messages(errs), tc.wantSub) {
				t.Errorf("errors %q do not mention %q", messages(errs), tc.wantSub)
			}
		})
	}
}

func TestValidateEnforcesActiveBookingCap(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")
	res := bike()
	res.Rules.MaxActivePerUser = 1

	first := store.Booking{
		ID: "a", ResourceID: res.ID,
		Start:  at(loc, "2026-05-03 10:00"),
		End:    at(loc, "2026-05-03 12:00"),
		Email:  "anna@example.se",
		Name:   "Anna",
		Status: store.StatusConfirmed, CreatedAt: now,
	}
	if err := st.Create(context.Background(), first, first.Start, first.End); err != nil {
		t.Fatalf("seed booking: %v", err)
	}

	_, _, errs := Validate(context.Background(), st, Request{
		Resource: res,
		Start:    at(loc, "2026-05-04 10:00"),
		End:      at(loc, "2026-05-04 12:00"),
		Name:     "Anna", Email: "ANNA@example.se",
	}, now, loc)

	if !strings.Contains(messages(errs), "aktiva bokningar") {
		t.Errorf("expected the per-member cap to fire, got %q", messages(errs))
	}
}

func TestValidateEnforcesWeeklyHourCap(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")
	res := bike()
	res.Rules.MaxHoursPerWeekPerUser = 6
	res.Rules.MaxActivePerUser = 0

	seed := store.Booking{
		ID: "a", ResourceID: res.ID,
		Start: at(loc, "2026-05-03 10:00"),
		End:   at(loc, "2026-05-03 14:00"),
		Email: "anna@example.se", Name: "Anna",
		Status: store.StatusConfirmed, CreatedAt: now,
	}
	if err := st.Create(context.Background(), seed, seed.Start, seed.End); err != nil {
		t.Fatalf("seed booking: %v", err)
	}

	_, _, errs := Validate(context.Background(), st, Request{
		Resource: res,
		Start:    at(loc, "2026-05-04 10:00"),
		End:      at(loc, "2026-05-04 14:00"), // 4 h more, over the 6 h cap
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if !strings.Contains(messages(errs), "gränsen är") {
		t.Errorf("expected the weekly hour cap to fire, got %q", messages(errs))
	}

	// Two hours fits inside the cap.
	_, _, errs = Validate(context.Background(), st, Request{
		Resource: res,
		Start:    at(loc, "2026-05-04 10:00"),
		End:      at(loc, "2026-05-04 12:00"),
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if len(errs) != 0 {
		t.Errorf("2 h should fit under the 6 h cap, got %q", messages(errs))
	}
}

func TestValidateDetectsOverlap(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")

	seed := store.Booking{
		ID: "a", ResourceID: "ellastcykel",
		Start: at(loc, "2026-05-03 10:00"),
		End:   at(loc, "2026-05-03 14:00"),
		Email: "bo@example.se", Name: "Bo",
		Status: store.StatusConfirmed, CreatedAt: now,
	}
	if err := st.Create(context.Background(), seed, seed.Start, seed.End); err != nil {
		t.Fatalf("seed booking: %v", err)
	}

	// 14:00 would touch the previous booking's 15 minute buffer.
	_, _, errs := Validate(context.Background(), st, Request{
		Resource: bike(),
		Start:    at(loc, "2026-05-03 14:00"),
		End:      at(loc, "2026-05-03 15:00"),
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if !strings.Contains(messages(errs), "bokad") {
		t.Errorf("expected a conflict inside the buffer, got %q", messages(errs))
	}

	// 14:30 clears the buffer.
	_, _, errs = Validate(context.Background(), st, Request{
		Resource: bike(),
		Start:    at(loc, "2026-05-03 14:30"),
		End:      at(loc, "2026-05-03 15:30"),
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if len(errs) != 0 {
		t.Errorf("14:30 clears the buffer but was rejected: %q", messages(errs))
	}
}

func TestValidateNightLimits(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")

	mk := func(from, to string) []Error {
		start, end := DayRange(room(), at(loc, from+" 00:00"), at(loc, to+" 00:00"), loc)
		_, _, errs := Validate(context.Background(), st, Request{
			Resource: room(), Start: start, End: end,
			Name: "Anna", Email: "anna@example.se",
		}, now, loc)
		return errs
	}

	if errs := mk("2026-05-10", "2026-05-13"); len(errs) != 0 {
		t.Errorf("3 nights should be fine, got %q", messages(errs))
	}
	if errs := mk("2026-05-10", "2026-05-25"); !strings.Contains(messages(errs), "Längsta bokning") {
		t.Errorf("15 nights should exceed the 7 night limit, got %q", messages(errs))
	}
}

func TestValidateRejectsDisabledResource(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	off := false
	res := bike()
	res.Enabled = &off

	_, _, errs := Validate(context.Background(), st, Request{
		Resource: res,
		Start:    at(loc, "2026-05-02 10:00"),
		End:      at(loc, "2026-05-02 14:00"),
		Name:     "Anna", Email: "anna@example.se",
	}, at(loc, "2026-05-01 08:00"), loc)

	if !strings.Contains(messages(errs), "går inte att boka") {
		t.Errorf("a disabled resource should be refused, got %q", messages(errs))
	}
}

func TestValidEmail(t *testing.T) {
	good := []string{"anna@example.se", "a.b+tag@sub.example.co.uk"}
	bad := []string{"", "anna", "anna@", "@example.se", "anna@example", "a b@example.se", "a@example.se, b@x.se"}
	for _, s := range good {
		if !validEmail(s) {
			t.Errorf("validEmail(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validEmail(s) {
			t.Errorf("validEmail(%q) = true, want false", s)
		}
	}
}
