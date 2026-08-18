package booking

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
)

// flexibleBike allows a typed-in length; the plain bike() does not.
func flexibleBike() config.Resource {
	r := bike()
	r.Rules.CustomDuration = true
	r.Rules.MinDurationMinutes = 30
	r.Rules.MaxDurationMinutes = 600
	return r
}

func TestParseHours(t *testing.T) {
	good := map[string]time.Duration{
		"1":    time.Hour,
		"2.5":  150 * time.Minute,
		"2,5":  150 * time.Minute, // Swedish decimal comma
		" 3 ":  3 * time.Hour,
		"0.5":  30 * time.Minute,
		"0,25": 15 * time.Minute,
		"10":   10 * time.Hour,
		"1.75": 105 * time.Minute,
	}
	for in, want := range good {
		got, err := ParseHours(in)
		if err != nil {
			t.Errorf("ParseHours(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseHours(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "  ", "abc", "-2", "0", "2h", "1.2.3"} {
		if _, err := ParseHours(bad); err == nil {
			t.Errorf("ParseHours(%q) should fail", bad)
		}
	}
}

// Floating point must never leak into a stored booking length.
func TestParseHoursRoundsToWholeMinutes(t *testing.T) {
	got, err := ParseHours("2.5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncate(time.Minute) != got {
		t.Errorf("%v is not a whole number of minutes", got)
	}
	// 0.1 h is 6 minutes, not 5 minutes 59.999 seconds.
	got, _ = ParseHours("0.1")
	if got != 6*time.Minute {
		t.Errorf("ParseHours(0.1) = %v, want 6m", got)
	}
}

func TestHoursParamRoundTrip(t *testing.T) {
	for mins := 5; mins <= 24*60; mins += 5 {
		d := time.Duration(mins) * time.Minute
		back, err := ParseHours(HoursParam(d))
		if err != nil {
			t.Fatalf("%v: %v", d, err)
		}
		if back != d {
			t.Errorf("%v round-tripped as %v via %q", d, back, HoursParam(d))
		}
	}
	// The common cases should look tidy in a URL.
	cases := map[time.Duration]string{
		time.Hour: "1", 90 * time.Minute: "1.5", 30 * time.Minute: "0.5", 8 * time.Hour: "8",
	}
	for d, want := range cases {
		if got := HoursParam(d); got != want {
			t.Errorf("HoursParam(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFormatHoursInputUsesSwedishComma(t *testing.T) {
	cases := map[time.Duration]string{
		time.Hour: "1", 90 * time.Minute: "1,5", 8 * time.Hour: "8", 45 * time.Minute: "0,75",
	}
	for d, want := range cases {
		if got := FormatHoursInput(d); got != want {
			t.Errorf("FormatHoursInput(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestCheckDurationWithoutCustomAllowsOnlyPresets(t *testing.T) {
	r := bike() // durations 1, 2, 4, 8; custom off
	for _, ok := range []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour} {
		if err := CheckDuration(r, ok); err != nil {
			t.Errorf("%v should be allowed: %s", ok, err.Message)
		}
	}
	for _, bad := range []time.Duration{90 * time.Minute, 3 * time.Hour, 30 * time.Minute} {
		err := CheckDuration(r, bad)
		if err == nil {
			t.Errorf("%v should be refused when custom lengths are off", bad)
			continue
		}
		if !strings.Contains(err.Message, "tillåtna längderna") {
			t.Errorf("unexpected message for %v: %s", bad, err.Message)
		}
	}
}

func TestCheckDurationWithCustomAllowsAnythingOnTheGrid(t *testing.T) {
	r := flexibleBike() // 30 min grid, 30 min – 10 h

	for _, ok := range []time.Duration{
		30 * time.Minute, time.Hour, 90 * time.Minute, 3 * time.Hour,
		5*time.Hour + 30*time.Minute, 10 * time.Hour,
	} {
		if err := CheckDuration(r, ok); err != nil {
			t.Errorf("%v should be allowed: %s", ok, err.Message)
		}
	}

	cases := []struct {
		dur     time.Duration
		wantSub string
	}{
		{15 * time.Minute, "Kortaste"},
		{11 * time.Hour, "Längsta"},
		{45 * time.Minute, "jämnt ut"},
		{2*time.Hour + 10*time.Minute, "jämnt ut"},
	}
	for _, c := range cases {
		err := CheckDuration(r, c.dur)
		if err == nil {
			t.Errorf("%v should be refused", c.dur)
			continue
		}
		if !strings.Contains(err.Message, c.wantSub) {
			t.Errorf("%v: message %q does not mention %q", c.dur, err.Message, c.wantSub)
		}
	}
}

// An off-grid length should suggest the nearest lengths that would work.
func TestCheckDurationSuggestsNeighbours(t *testing.T) {
	err := CheckDuration(flexibleBike(), 45*time.Minute)
	if err == nil {
		t.Fatal("45 min is off a 30 min grid and should be refused")
	}
	if !strings.Contains(err.Message, "30 min") || !strings.Contains(err.Message, "1 h") {
		t.Errorf("message should offer 30 min and 1 h: %s", err.Message)
	}
}

// A preset stays valid even when it falls outside the custom bounds, so
// tightening the bounds cannot silently break the buttons.
func TestPresetsBeatCustomBounds(t *testing.T) {
	r := flexibleBike()
	r.Rules.MaxDurationMinutes = 60
	if err := CheckDuration(r, 8*time.Hour); err != nil {
		t.Errorf("the 8 h preset should still be bookable: %s", err.Message)
	}
	if err := CheckDuration(r, 3*time.Hour); err == nil {
		t.Error("3 h is not a preset and is over the custom limit; it should be refused")
	}
}

func TestIsPreset(t *testing.T) {
	r := flexibleBike()
	if !IsPreset(r, 4*time.Hour) {
		t.Error("4 h is a preset")
	}
	if IsPreset(r, 3*time.Hour) {
		t.Error("3 h is not a preset")
	}
}

func TestValidateAcceptsACustomLength(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")

	_, _, errs := Validate(context.Background(), st, Request{
		Resource: flexibleBike(),
		Start:    at(loc, "2026-05-02 10:00"),
		End:      at(loc, "2026-05-02 13:30"), // 3.5 h, not a preset
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if len(errs) != 0 {
		t.Errorf("a 3.5 h booking should be accepted: %s", messages(errs))
	}

	// The same request against the strict bike is refused.
	_, _, errs = Validate(context.Background(), st, Request{
		Resource: bike(),
		Start:    at(loc, "2026-05-03 10:00"),
		End:      at(loc, "2026-05-03 13:30"),
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if len(errs) == 0 {
		t.Error("3.5 h should be refused when custom lengths are off")
	}
}

// A custom length still has to fit inside the opening hours.
func TestCustomLengthStillRespectsOpeningHours(t *testing.T) {
	loc := stockholm(t)
	st := newStore(t)
	now := at(loc, "2026-05-01 08:00")

	_, _, errs := Validate(context.Background(), st, Request{
		Resource: flexibleBike(), // open 06:00–22:00
		Start:    at(loc, "2026-05-02 19:00"),
		End:      at(loc, "2026-05-02 23:30"), // 4.5 h, runs past closing
		Name:     "Anna", Email: "anna@example.se",
	}, now, loc)
	if !strings.Contains(messages(errs), "kan bokas mellan") {
		t.Errorf("expected an opening-hours complaint, got %q", messages(errs))
	}
}
