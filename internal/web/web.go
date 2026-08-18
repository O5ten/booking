// Package web serves the booking site: server-rendered HTML, no build step,
// no client-side framework.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/auth"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/booking"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/mail"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server holds everything the handlers need.
type Server struct {
	cfg    *config.Config
	rt     config.Runtime
	store  *store.Store
	guard  *auth.Guard
	mailer *mail.Sender
	log    *slog.Logger
	tpl    map[string]*template.Template
	now    func() time.Time
}

// pages are the top-level templates. Each is parsed into its own set together
// with the shared layout, because every page defines a "content" block and Go
// templates share one namespace per set.
var pages = []string{
	"index.html", "login.html", "error.html", "booking.html", "mine.html",
	"admin.html", "resource_hours.html", "resource_days.html", "upcoming.html",
}

// layouts are included in every page set.
var layouts = []string{"base.html", "fields.html"}

// New builds the HTTP server.
func New(cfg *config.Config, rt config.Runtime, st *store.Store, guard *auth.Guard, mailer *mail.Sender, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, rt: rt, store: st, guard: guard, mailer: mailer, log: log, now: time.Now}
	s.tpl = make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		files := make([]string, 0, len(layouts)+1)
		for _, l := range layouts {
			files = append(files, "templates/"+l)
		}
		files = append(files, "templates/"+page)
		t, err := template.New(page).Funcs(s.funcs()).ParseFS(templateFS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		s.tpl[page] = t
	}
	return s, nil
}

// Handler returns the router with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(fileServer)))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.Handle("GET /{$}", s.member(s.handleIndex))
	mux.Handle("GET /resurs/{id}", s.member(s.handleResource))
	mux.Handle("GET /resurs/{id}/bokningar", s.member(s.handleResourceUpcoming))
	mux.Handle("POST /resurs/{id}/boka", s.member(s.handleCreateBooking))
	mux.Handle("GET /bokning/{id}", s.member(s.handleBooking))
	mux.Handle("GET /bokning/{id}/kalender.ics", s.member(s.handleBookingICS))
	mux.Handle("POST /bokning/{id}/avboka", s.member(s.handleCancel))
	mux.Handle("GET /mina", s.member(s.handleMyBookings))
	mux.Handle("GET /kalender/{id}/flode.ics", s.member(s.handleResourceFeed))

	mux.Handle("GET /admin", s.admin(s.handleAdmin))
	mux.Handle("GET /admin/export.csv", s.admin(s.handleAdminCSV))
	mux.Handle("POST /admin/avboka/{id}", s.admin(s.handleAdminCancel))

	return s.recoverPanic(securityHeaders(mux))
}

// ctxKey namespaces the values the middleware puts on the request context.
type ctxKey string

const (
	ctxRole  ctxKey = "role"
	ctxIdent ctxKey = "ident"
)

// member wraps a handler so only logged-in members reach it.
func (s *Server) member(h func(http.ResponseWriter, *http.Request, *view)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := s.guard.Role(r)
		if !role.LoggedIn() {
			next := r.URL.RequestURI()
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		h(w, r, s.newView(r, role))
	})
}

// admin wraps a handler so only the admin password reaches it.
func (s *Server) admin(h func(http.ResponseWriter, *http.Request, *view)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := s.guard.Role(r)
		if !role.LoggedIn() {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if !role.Admin() {
			s.renderError(w, r, http.StatusForbidden,
				"Bara husets administratör kommer åt den här sidan.",
				"Logga in igen med administratörslösenordet om du behöver se alla bokningar.")
			return
		}
		h(w, r, s.newView(r, role))
	})
}

// view is the data every page shares.
type view struct {
	Site      config.Site
	Role      auth.Role
	Ident     auth.Identity
	Now       time.Time
	Loc       *time.Location
	Path      string
	Title     string
	HasAdmin  bool
	MailOn    bool
	Demo      bool
	DemoPass  string
	DemoAdmin string
	Flash     string
	FlashKind string
	Data      any
}

func (s *Server) newView(r *http.Request, role auth.Role) *view {
	return &view{
		Site:     s.cfg.Site,
		Role:     role,
		Ident:    s.guard.Identity(r),
		Now:      s.now().In(s.cfg.Location()),
		Loc:      s.cfg.Location(),
		Path:     r.URL.Path,
		HasAdmin: s.guard.HasAdmin(),
		MailOn:   s.mailer.Enabled(),

		Demo:      s.rt.Demo,
		DemoPass:  s.rt.Password,
		DemoAdmin: s.rt.AdminPassword,
	}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, v *view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	t, ok := s.tpl[name]
	if !ok {
		s.log.Error("unknown template", "template", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Render into a buffer so a mid-template failure cannot emit half a page.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Error("render template", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, headline, detail string) {
	v := s.newView(r, s.guard.Role(r))
	v.Title = headline
	v.Data = map[string]any{"Headline": headline, "Detail": detail, "Status": status}
	s.render(w, r, status, "error.html", v)
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"weekday":      Weekday,
		"weekdayShort": WeekdayShort,
		"month":        Month,
		"monthYear":    MonthYear,
		"dateLong":     DateLong,
		"dateLongYear": DateLongYear,
		"dateShort":    DateShort,
		"clock":        Clock,
		"isoDate":      ISODate,
		"interval":     Interval,
		"relativeDay":  RelativeDay,
		"nights":       Nights,
		"titleCase":    TitleCase,
		"duration":     booking.FormatDuration,
		"durationList": booking.DurationList,
		"local": func(t time.Time) time.Time {
			return t.In(s.cfg.Location())
		},
		"pct": func(f float64) template.CSS {
			return template.CSS(fmt.Sprintf("%.4f%%", f))
		},
		"add":  func(a, b int) int { return a + b },
		"dict": dict,
		"query": func(base string, pairs ...string) template.URL {
			q := url.Values{}
			for i := 0; i+1 < len(pairs); i += 2 {
				if pairs[i+1] != "" {
					q.Set(pairs[i], pairs[i+1])
				}
			}
			if len(q) == 0 {
				return template.URL(base)
			}
			return template.URL(base + "?" + q.Encode())
		},
		"hasPrefix": strings.HasPrefix,
	}
}

func dict(pairs ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		m[key] = pairs[i+1]
	}
	return m
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// Everything is served from this origin; no external scripts or styles.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic serving request", "path", r.URL.Path, "err", rec)
				s.renderError(w, r, http.StatusInternalServerError,
					"Något gick fel", "Försök igen, eller hör av dig till husets datorgrupp om det fortsätter.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the caller's address, honouring X-Forwarded-For when the
// deployment sits behind a reverse proxy.
func (s *Server) clientIP(r *http.Request) string {
	if s.rt.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
