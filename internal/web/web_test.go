package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/auth"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/mail"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

const testConfig = `
site:
  title: Rudbeckia bokning
  timezone: Europe/Stockholm
categories:
  - id: cyklar
    name: Cyklar
  - id: gastrum
    name: Gästrum
resources:
  - id: ellastcykel
    category: cyklar
    name: Ellastcykeln
    location: Cykelrummet
    booking:
      mode: hours
      durations: [1, 2, 4, 8]
      slot_step_minutes: 30
      buffer_minutes: 15
      open_from: "06:00"
      open_to: "22:00"
      max_advance_days: 30
      max_active_per_user: 2
  - id: gastrum-1
    category: gastrum
    name: Gästrum 1
    booking:
      mode: days
      min_days: 1
      max_days: 7
      max_advance_days: 180
  - id: avstangd
    category: cyklar
    name: Avstängd cykel
    enabled: false
    booking:
      mode: hours
`

// harness is a fully wired server with a frozen clock, so tests can talk about
// concrete dates.
type harness struct {
	*testing.T
	server *Server
	mux    http.Handler
	store  *store.Store
	now    time.Time
	loc    *time.Location
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(testConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	rt := config.Runtime{BaseURL: "https://booking.rudbeckia.nu", TrustProxy: true}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	guard := auth.New("husets-losenord", "admin-losenord", []byte("secret-for-tests"), time.Hour, false)
	srv, err := New(cfg, rt, st, guard, mail.NewSender(config.MailSettings{}, log), log)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	loc := cfg.Location()
	// A Monday morning, well clear of any DST boundary.
	now := time.Date(2026, 5, 4, 8, 0, 0, 0, loc)
	srv.now = func() time.Time { return now }

	return &harness{T: t, server: srv, mux: srv.Handler(), store: st, now: now, loc: loc}
}

// login returns the session cookies for a password.
func (h *harness) login(password string) []*http.Cookie {
	h.Helper()
	form := url.Values{"password": {password}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		h.Fatalf("login as %q: status %d", password, rec.Code)
	}
	return rec.Result().Cookies()
}

func (h *harness) do(method, target string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	h.Helper()
	var req *http.Request
	if form == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *harness) date(offsetDays int) string {
	return h.now.AddDate(0, 0, offsetDays).Format("2006-01-02")
}

func TestEverythingIsBehindThePassword(t *testing.T) {
	h := newHarness(t)
	protected := []string{"/", "/resurs/ellastcykel", "/mina", "/admin",
		"/bokning/whatever", "/kalender/ellastcykel/flode.ics"}

	for _, path := range protected {
		rec := h.do("GET", path, nil, nil)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s without a session = %d, want a redirect to /login", path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Errorf("GET %s redirected to %q, want /login", path, loc)
		}
	}

	// The login page and health check stay open.
	for _, path := range []string{"/login", "/healthz"} {
		if rec := h.do("GET", path, nil, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestLoginRejectsTheWrongPassword(t *testing.T) {
	h := newHarness(t)
	rec := h.do("POST", "/login", url.Values{"password": {"gissning"}}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Fel lösenord") {
		t.Error("the page should say the password was wrong")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a failed login must not set a session cookie")
	}
}

func TestLoginRedirectsOnlyWithinTheSite(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"/mina":               "/mina",
		"":                    "/",
		"https://example.com": "/",
		"//example.com":       "/",
	}
	for next, want := range cases {
		rec := h.do("POST", "/login", url.Values{"password": {"husets-losenord"}, "next": {next}}, nil)
		if got := rec.Header().Get("Location"); got != want {
			t.Errorf("next=%q redirected to %q, want %q", next, got, want)
		}
	}
}

func TestMemberSeesTheResourcesButNotTheAdminView(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")

	body := h.do("GET", "/", nil, member).Body.String()
	for _, want := range []string{"Ellastcykeln", "Gästrum 1", "Cyklar"} {
		if !strings.Contains(body, want) {
			t.Errorf("the start page is missing %q", want)
		}
	}
	if strings.Contains(body, "Avstängd cykel") {
		t.Error("a disabled resource must not be offered")
	}
	if strings.Contains(body, "Alla bokningar") {
		t.Error("members should not see the admin link")
	}

	if rec := h.do("GET", "/admin", nil, member); rec.Code != http.StatusForbidden {
		t.Errorf("member reaching /admin = %d, want 403", rec.Code)
	}
}

func TestDisabledResourcePageIsNotFound(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	if rec := h.do("GET", "/resurs/avstangd", nil, member); rec.Code != http.StatusNotFound {
		t.Errorf("disabled resource = %d, want 404", rec.Code)
	}
	if rec := h.do("GET", "/resurs/finns-inte", nil, member); rec.Code != http.StatusNotFound {
		t.Errorf("unknown resource = %d, want 404", rec.Code)
	}
}

// The whole path a member walks: pick a slot, book it, get a confirmation,
// see the slot disappear, then cancel it and see it come back.
func TestBookingLifecycle(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	tomorrow := h.date(1)

	page := h.do("GET", "/resurs/ellastcykel?datum="+tomorrow+"&langd=4", nil, member).Body.String()
	if !strings.Contains(page, ">10:00<") {
		t.Fatal("10:00 should be offered on an empty day")
	}

	form := url.Values{
		"datum": {tomorrow}, "start": {"10:00"}, "langd": {"4"},
		"name": {"Anna Andersson"}, "apartment": {"1403"},
		"email": {"anna@example.se"}, "note": {"Storhandling"}, "remember": {"1"},
	}
	rec := h.do("POST", "/resurs/ellastcykel/boka", form, member)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("booking = %d, want a redirect. Body:\n%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/bokning/") {
		t.Fatalf("redirected to %q", location)
	}
	// The member's details are remembered for next time.
	session := append(member, rec.Result().Cookies()...)

	confirm := h.do("GET", location, nil, session).Body.String()
	for _, want := range []string{"Klart", "Ellastcykeln", "10:00", "14:00", "Storhandling",
		"calendar.google.com", "Cykelrummet"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("the confirmation page is missing %q", want)
		}
	}

	// The slot is gone, and the buffer around it too.
	page = h.do("GET", "/resurs/ellastcykel?datum="+tomorrow+"&langd=1", nil, session).Body.String()
	if strings.Count(page, "Din bokning") == 0 {
		t.Error("the member's own booking should be labelled on the grid")
	}

	id := strings.TrimSuffix(strings.TrimPrefix(location, "/bokning/"), "?ny=1")

	// It shows up under "my bookings" thanks to the remembered address.
	mine := h.do("GET", "/mina", nil, session).Body.String()
	if !strings.Contains(mine, id) {
		t.Error("the booking is missing from /mina")
	}

	// The .ics download carries the right times.
	ics := h.do("GET", "/bokning/"+id+"/kalender.ics", nil, session)
	if ct := ics.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("ics content type = %q", ct)
	}
	if !strings.Contains(ics.Body.String(), "DTSTART:20260505T080000Z") {
		t.Errorf("ics start time is wrong:\n%s", ics.Body.String())
	}

	// Cancelling needs the token from the confirmation page.
	token := between(confirm, `name="token" value="`, `"`)
	if token == "" {
		t.Fatal("no cancel token on the confirmation page")
	}
	if rec := h.do("POST", "/bokning/"+id+"/avboka", url.Values{"token": {"fel"}}, h.login("husets-losenord")); rec.Code != http.StatusForbidden {
		t.Errorf("cancelling with a bad token = %d, want 403", rec.Code)
	}
	if rec := h.do("POST", "/bokning/"+id+"/avboka", url.Values{"token": {token}}, session); rec.Code != http.StatusSeeOther {
		t.Errorf("cancelling = %d, want a redirect", rec.Code)
	}

	// And the slot is bookable again.
	page = h.do("GET", "/resurs/ellastcykel?datum="+tomorrow+"&langd=4", nil, session).Body.String()
	if !strings.Contains(page, ">10:00<") {
		t.Error("the cancelled slot should be free again")
	}
}

func TestDoubleBookingIsRefused(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	tomorrow := h.date(1)

	form := url.Values{
		"datum": {tomorrow}, "start": {"10:00"}, "langd": {"4"},
		"name": {"Anna"}, "email": {"anna@example.se"},
	}
	if rec := h.do("POST", "/resurs/ellastcykel/boka", form, member); rec.Code != http.StatusSeeOther {
		t.Fatalf("first booking = %d", rec.Code)
	}

	form.Set("name", "Bo")
	form.Set("email", "bo@example.se")
	form.Set("start", "12:00")
	form.Set("langd", "1")
	rec := h.do("POST", "/resurs/ellastcykel/boka", form, member)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("overlapping booking = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bokad") {
		t.Error("the page should explain that the time is taken")
	}
}

func TestInvalidBookingKeepsTheFormFilledIn(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")

	form := url.Values{
		"datum": {h.date(1)}, "start": {"10:00"}, "langd": {"4"},
		"name": {"Anna Andersson"}, "apartment": {"1403"}, "email": {"trasig-adress"},
	}
	rec := h.do("POST", "/resurs/ellastcykel/boka", form, member)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "giltig e-postadress") {
		t.Error("the validation message is missing")
	}
	for _, want := range []string{`value="Anna Andersson"`, `value="1403"`, `value="trasig-adress"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form lost %s, so the member would have to retype it", want)
		}
	}
}

func TestGuestRoomBookingUsesNights(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")

	form := url.Values{
		"fran": {h.date(10)}, "till": {h.date(13)},
		"name": {"Anna"}, "email": {"anna@example.se"},
	}
	rec := h.do("POST", "/resurs/gastrum-1/boka", form, member)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("booking = %d. Body:\n%s", rec.Code, rec.Body.String())
	}
	confirm := h.do("GET", rec.Header().Get("Location"), nil, member).Body.String()
	if !strings.Contains(confirm, "3 nätter") {
		t.Error("the confirmation should say three nights")
	}
	if !strings.Contains(confirm, "15:00") || !strings.Contains(confirm, "12:00") {
		t.Error("check-in and check-out times are missing from the confirmation")
	}

	// Those nights are now blocked on the calendar.
	page := h.do("GET", "/resurs/gastrum-1", nil, member).Body.String()
	if strings.Count(page, "cal-day is-taken") != 3 {
		t.Errorf("expected exactly 3 booked nights on the calendar, got %d",
			strings.Count(page, "cal-day is-taken"))
	}
}

func TestAdminSeesEveryBookingAndCanCancel(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	admin := h.login("admin-losenord")

	form := url.Values{
		"datum": {h.date(1)}, "start": {"10:00"}, "langd": {"4"},
		"name": {"Anna Andersson"}, "email": {"anna@example.se"}, "apartment": {"1403"},
	}
	rec := h.do("POST", "/resurs/ellastcykel/boka", form, member)
	id := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/bokning/"), "?ny=1")

	body := h.do("GET", "/admin", nil, admin).Body.String()
	for _, want := range []string{"Anna Andersson", "anna@example.se", "1403", "Ellastcykeln", "bekräftad"} {
		if !strings.Contains(body, want) {
			t.Errorf("the admin view is missing %q", want)
		}
	}

	csv := h.do("GET", "/admin/export.csv?period=all", nil, admin)
	if ct := csv.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("csv content type = %q", ct)
	}
	if !strings.Contains(csv.Body.String(), "anna@example.se") {
		t.Error("the export is missing the booking")
	}

	// The admin can cancel without holding anyone's token.
	if rec := h.do("POST", "/admin/avboka/"+id, nil, admin); rec.Code != http.StatusSeeOther {
		t.Errorf("admin cancel = %d, want a redirect", rec.Code)
	}
	body = h.do("GET", "/admin?period=all", nil, admin).Body.String()
	if !strings.Contains(body, "avbokad") {
		t.Error("the cancelled booking should be listed as cancelled")
	}
}

func TestAdminFilters(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	admin := h.login("admin-losenord")

	h.do("POST", "/resurs/ellastcykel/boka", url.Values{
		"datum": {h.date(1)}, "start": {"10:00"}, "langd": {"2"},
		"name": {"Anna Andersson"}, "email": {"anna@example.se"},
	}, member)
	h.do("POST", "/resurs/gastrum-1/boka", url.Values{
		"fran": {h.date(10)}, "till": {h.date(12)},
		"name": {"Bo Bengtsson"}, "email": {"bo@example.se"},
	}, member)

	byResource := h.do("GET", "/admin?resurs=gastrum-1", nil, admin).Body.String()
	if !strings.Contains(byResource, "Bo Bengtsson") || strings.Contains(byResource, "Anna Andersson") {
		t.Error("filtering by resource did not narrow the list")
	}

	bySearch := h.do("GET", "/admin?sok=andersson", nil, admin).Body.String()
	if !strings.Contains(bySearch, "Anna Andersson") || strings.Contains(bySearch, "Bo Bengtsson") {
		t.Error("the free-text search did not narrow the list")
	}
}

func TestResourceFeedListsBookings(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	h.do("POST", "/resurs/ellastcykel/boka", url.Values{
		"datum": {h.date(1)}, "start": {"10:00"}, "langd": {"2"},
		"name": {"Anna"}, "email": {"anna@example.se"},
	}, member)

	rec := h.do("GET", "/kalender/ellastcykel/flode.ics", nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Count(body, "BEGIN:VEVENT") != 1 {
		t.Errorf("feed has %d events, want 1", strings.Count(body, "BEGIN:VEVENT"))
	}
	if !strings.Contains(body, "X-WR-CALNAME:Rudbeckia bokning – Ellastcykeln") {
		t.Error("the feed should be named after the resource")
	}
}

func TestSecurityHeadersAndNoIndexing(t *testing.T) {
	h := newHarness(t)
	rec := h.do("GET", "/login", nil, nil)
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if !strings.Contains(rec.Body.String(), `name="robots" content="noindex`) {
		t.Error("pages should ask search engines to stay away")
	}
}

func TestLogoutClearsTheSession(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")
	rec := h.do("POST", "/logout", nil, member)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge >= 0 {
		t.Error("logout should expire the session cookie")
	}
	if rec := h.do("GET", "/", nil, cleared); rec.Code != http.StatusSeeOther {
		t.Error("the cleared cookie should no longer grant access")
	}
}

// between returns the text between two markers, or "".
func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// Landing on a resource whose day is already fully booked should jump to the
// next day that has something free, not show an empty grid.
func TestResourcePageSkipsToTheFirstFreeDay(t *testing.T) {
	h := newHarness(t)
	member := h.login("husets-losenord")

	// Fill tomorrow completely with back-to-back bookings.
	day := h.now.AddDate(0, 0, 1)
	for hour := 6; hour < 22; hour += 2 {
		b := storeBooking(h, day, hour, 2)
		if err := h.store.Create(context.Background(), b, b.Start, b.End); err != nil {
			t.Fatalf("seed %d: %v", hour, err)
		}
	}

	// Today is already past its opening hours in this harness? No: the clock is
	// 08:00, so today has free slots and should be chosen.
	body := h.do("GET", "/resurs/ellastcykel", nil, member).Body.String()
	if !strings.Contains(body, h.now.Format("2006-01-02")) {
		t.Error("with free slots today, the page should open on today")
	}

	// Now block today as well; the page must skip forward past tomorrow.
	today := h.now
	for hour := 8; hour < 22; hour += 2 {
		b := storeBooking(h, today, hour, 2)
		if err := h.store.Create(context.Background(), b, b.Start, b.End); err != nil {
			t.Fatalf("seed today %d: %v", hour, err)
		}
	}
	body = h.do("GET", "/resurs/ellastcykel", nil, member).Body.String()
	dayAfter := h.date(2)
	if !strings.Contains(body, "Lediga starttider") {
		t.Fatal("the page should still render a slot list")
	}
	if !strings.Contains(body, dayAfter) {
		t.Errorf("expected the page to skip to %s, which is the first free day", dayAfter)
	}
	if strings.Contains(body, "Inget ledigt den här dagen") {
		t.Error("the page landed on a day with nothing free")
	}
}

func storeBooking(h *harness, day time.Time, hour, length int) store.Booking {
	start := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, h.loc)
	return store.Booking{
		ID:         start.Format("150405.000") + "-" + day.Format("0102"),
		ResourceID: "ellastcykel",
		Start:      start,
		End:        start.Add(time.Duration(length) * time.Hour),
		Mode:       "hours",
		Name:       "Någon Annan",
		Email:      "annan@example.se",
		Status:     store.StatusConfirmed,
		CreatedAt:  h.now,
	}
}
