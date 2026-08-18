package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const minimal = `
site:
  title: Test
categories:
  - id: cyklar
    name: Cyklar
resources:
  - id: elcykel
    category: cyklar
    name: Elcykeln
    booking:
      mode: hours
`

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(write(t, minimal))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Site.Timezone != "Europe/Stockholm" {
		t.Errorf("timezone = %q, want the Stockholm default", cfg.Site.Timezone)
	}
	if cfg.Location() == nil {
		t.Fatal("location was not resolved")
	}
	r, ok := cfg.Resource("elcykel")
	if !ok {
		t.Fatal("resource elcykel missing")
	}
	if got, want := len(r.Rules.Durations), 4; got != want {
		t.Errorf("durations = %v, want the 1/2/4/8 default", r.Rules.Durations)
	}
	if r.Rules.SlotStepMinutes != 30 || r.Rules.MaxAdvanceDays != 90 {
		t.Errorf("defaults not applied: %+v", r.Rules)
	}
	if r.Rules.OpenFrom != "00:00" || r.Rules.OpenTo != "24:00" {
		t.Errorf("opening hours = %s–%s, want the full day", r.Rules.OpenFrom, r.Rules.OpenTo)
	}
	if !r.Active() {
		t.Error("a resource without an explicit enabled flag should be active")
	}
}

func TestLoadDayModeDefaults(t *testing.T) {
	cfg, err := Load(write(t, `
site:
  title: Test
resources:
  - id: gastrum
    name: Gästrum
    booking:
      mode: days
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r, _ := cfg.Resource("gastrum")
	if r.Rules.MinDays != 1 || r.Rules.MaxDays != 14 {
		t.Errorf("night limits = %d–%d, want 1–14", r.Rules.MinDays, r.Rules.MaxDays)
	}
	if r.Rules.CheckIn != "15:00" || r.Rules.CheckOut != "12:00" {
		t.Errorf("check times = %s/%s", r.Rules.CheckIn, r.Rules.CheckOut)
	}
}

func TestLoadSortsDurations(t *testing.T) {
	cfg, err := Load(write(t, `
site:
  title: Test
resources:
  - id: cykel
    name: Cykel
    booking:
      durations: [8, 1, 4, 2]
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r, _ := cfg.Resource("cykel")
	want := []float64{1, 2, 4, 8}
	for i, d := range want {
		if r.Rules.Durations[i] != d {
			t.Fatalf("durations = %v, want sorted %v", r.Rules.Durations, want)
		}
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"duplicate resource id": `
site: {title: T}
resources:
  - {id: a, name: A}
  - {id: a, name: B}
`,
		"unknown category": `
site: {title: T}
resources:
  - {id: a, name: A, category: nope}
`,
		"missing id": `
site: {title: T}
resources:
  - {name: A}
`,
		"unknown mode": `
site: {title: T}
resources:
  - {id: a, name: A, booking: {mode: weeks}}
`,
		"closing before opening": `
site: {title: T}
resources:
  - {id: a, name: A, booking: {open_from: "20:00", open_to: "08:00"}}
`,
		"bad clock": `
site: {title: T}
resources:
  - {id: a, name: A, booking: {open_from: "25:00"}}
`,
		"max nights below min": `
site: {title: T}
resources:
  - {id: a, name: A, booking: {mode: days, min_days: 5, max_days: 2}}
`,
		"unknown timezone": `
site: {title: T, timezone: Mars/Olympus}
resources: []
`,
		"unknown field": `
site: {title: T}
resources:
  - {id: a, name: A, colour: blue}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

func TestGroupedKeepsConfigOrderAndDropsEmpties(t *testing.T) {
	cfg, err := Load(write(t, `
site: {title: T}
categories:
  - {id: cyklar, name: Cyklar}
  - {id: tomma, name: Tomma}
  - {id: rum, name: Rum}
resources:
  - {id: a, name: A, category: cyklar}
  - {id: b, name: B, category: rum}
  - {id: c, name: C}
  - {id: d, name: D, category: cyklar, enabled: false}
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	groups := cfg.Grouped()
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want cyklar, rum and the loose bucket", len(groups))
	}
	if groups[0].Category.ID != "cyklar" || groups[1].Category.ID != "rum" {
		t.Errorf("groups out of config order: %s, %s", groups[0].Category.ID, groups[1].Category.ID)
	}
	if len(groups[0].Resources) != 1 {
		t.Errorf("the disabled resource should be hidden, got %d", len(groups[0].Resources))
	}
	if groups[2].Category.Name != "Övrigt" {
		t.Errorf("uncategorised resources should fall into Övrigt, got %q", groups[2].Category.Name)
	}
}

func TestParseAndFormatClock(t *testing.T) {
	cases := map[string]int{"00:00": 0, "06:30": 390, "15:00": 900, "24:00": 1440}
	for in, want := range cases {
		got, err := ParseClock(in)
		if err != nil {
			t.Fatalf("ParseClock(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseClock(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "8", "8:00:00", "24:30", "-1:00", "aa:bb", "12:99"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("ParseClock(%q) should fail", bad)
		}
	}
	if got := FormatClock(390); got != "06:30" {
		t.Errorf("FormatClock(390) = %q, want 06:30", got)
	}
}

func TestLoadReportsMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Errorf("err = %v, want a read failure", err)
	}
}
