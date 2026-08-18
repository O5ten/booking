package ical

import (
	"strings"
	"testing"
	"time"
)

func event() Event {
	start := time.Date(2026, 5, 2, 8, 0, 0, 0, time.UTC)
	return Event{
		UID:         "abc123@booking.rudbeckia.nu",
		Summary:     "Ellastcykeln – Anna Andersson",
		Description: "Bokad av Anna; lgh 1403\nStorhandling",
		Location:    "Cykelrummet i källaren",
		Start:       start,
		End:         start.Add(4 * time.Hour),
		Created:     start.Add(-24 * time.Hour),
		URL:         "https://booking.rudbeckia.nu/bokning/abc123",
	}
}

func TestCalendarStructure(t *testing.T) {
	out := string(Calendar("Ellastcykeln", []Event{event()}))

	for _, want := range []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "END:VCALENDAR",
		"BEGIN:VEVENT", "END:VEVENT",
		"UID:abc123@booking.rudbeckia.nu",
		"DTSTART:20260502T080000Z",
		"DTEND:20260502T120000Z",
		"STATUS:CONFIRMED",
		"X-WR-CALNAME:Ellastcykeln",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("calendar is missing %q", want)
		}
	}
	if !strings.HasSuffix(out, "END:VCALENDAR\r\n") {
		t.Error("calendar must end with CRLF")
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line exceeds the 75 octet limit (%d): %q", len(line), line)
		}
	}
}

func TestCalendarEscapesSpecialCharacters(t *testing.T) {
	e := event()
	e.Summary = `Fest; med, komma \ och backslash`
	out := string(Calendar("", []Event{e}))
	if !strings.Contains(out, `Fest\; med\, komma \\ och backslash`) {
		t.Errorf("special characters were not escaped:\n%s", out)
	}
	// Newlines in the description become the literal two-character \n sequence.
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(unfolded, `Bokad av Anna\; lgh 1403\nStorhandling`) {
		t.Errorf("description newline was not escaped:\n%s", out)
	}
}

// Folding must not split a UTF-8 rune in half, or calendars show mojibake.
func TestFoldKeepsRunesIntact(t *testing.T) {
	e := event()
	e.Description = strings.Repeat("åäöéü", 40)
	out := string(Calendar("", []Event{e}))
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(unfolded, strings.Repeat("åäöéü", 40)) {
		t.Error("unfolding did not restore the original text")
	}
	for _, line := range strings.Split(out, "\r\n") {
		if !isValidUTF8(line) {
			t.Fatalf("folded line contains a broken rune: %q", line)
		}
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

func TestCancelledStatus(t *testing.T) {
	e := event()
	e.Cancelled = true
	if !strings.Contains(string(Calendar("", []Event{e})), "STATUS:CANCELLED") {
		t.Error("cancelled events must carry STATUS:CANCELLED")
	}
}

func TestMultipleEvents(t *testing.T) {
	out := string(Calendar("Feed", []Event{event(), event()}))
	if got := strings.Count(out, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("got %d events, want 2", got)
	}
}

func TestGoogleLink(t *testing.T) {
	link := GoogleLink(event())
	for _, want := range []string{
		"https://calendar.google.com/calendar/render?",
		"action=TEMPLATE",
		"dates=20260502T080000Z%2F20260502T120000Z",
		"ctz=Europe%2FStockholm",
	} {
		if !strings.Contains(link, want) {
			t.Errorf("google link is missing %q:\n%s", want, link)
		}
	}
}

func TestOutlookLink(t *testing.T) {
	link := OutlookLink(event())
	if !strings.HasPrefix(link, "https://outlook.office.com/calendar/0/deeplink/compose?") {
		t.Errorf("unexpected outlook link: %s", link)
	}
	if !strings.Contains(link, "startdt=2026-05-02T08%3A00%3A00Z") {
		t.Errorf("outlook link is missing the start time: %s", link)
	}
}

func TestFilenameIsSafe(t *testing.T) {
	got := Filename("Gästrum 1", time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC))
	if got != "g-strum-1-2026-05-02.ics" {
		t.Errorf("Filename = %q", got)
	}
	if strings.ContainsAny(got, `/\ "`) {
		t.Errorf("filename %q contains characters that break Content-Disposition", got)
	}
}
