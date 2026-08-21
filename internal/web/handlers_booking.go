package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/auth"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/booking"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/ical"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

func (s *Server) handleCreateBooking(w http.ResponseWriter, r *http.Request, v *view) {
	res, ok := s.cfg.Resource(r.PathValue("id"))
	if !ok || !res.Active() {
		s.renderError(w, r, http.StatusNotFound, "Resursen finns inte", "Kontrollera länken.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formuläret kunde inte läsas", "Försök igen.")
		return
	}
	now := s.now()
	loc := s.cfg.Location()

	form := bookingForm{
		Name:      strings.TrimSpace(r.FormValue("name")),
		Apartment: strings.TrimSpace(r.FormValue("apartment")),
		// Kept as typed: the resolver understands names as well as usernames,
		// and if it fails the member sees their own words back in the field.
		Member:   strings.TrimSpace(r.FormValue("medlem")),
		Phone:    strings.TrimSpace(r.FormValue("phone")),
		Note:     strings.TrimSpace(r.FormValue("note")),
		Remember: r.FormValue("remember") != "",
	}

	// Who is booking? The form names a Mattermost account; everything else
	// about the member comes from there, including where to send the
	// confirmation.
	who, memberErrs := s.resolveMember(r.Context(), form.Member)
	if form.Name == "" {
		form.Name = who.DisplayName()
	}

	start, end, parseErrs := s.parseInterval(res, r, loc)
	errs := append(memberErrs, parseErrs...)
	if len(errs) == 0 {
		req := booking.Request{
			Resource: res, Start: start, End: end,
			Name: form.Name, Apartment: form.Apartment,
			MMUsername: who.Username, MMUserID: who.ID,
			Email: who.Email, Phone: form.Phone, Note: form.Note,
		}
		blockFrom, blockTo, verrs := booking.Validate(r.Context(), s.store, req, now, loc)
		errs = verrs
		if len(errs) == 0 {
			b := store.Booking{
				ID:          auth.ID(),
				ResourceID:  res.ID,
				Start:       start,
				End:         end,
				Mode:        string(res.Rules.Mode),
				Name:        form.Name,
				Apartment:   form.Apartment,
				MMUsername:  who.Username,
				MMUserID:    who.ID,
				Email:       who.Email,
				Phone:       form.Phone,
				Note:        form.Note,
				Status:      store.StatusConfirmed,
				CancelToken: auth.Token(),
				CreatedAt:   now,
				CreatedIP:   s.clientIP(r),
			}
			err := s.store.Create(r.Context(), b, blockFrom, blockTo)
			switch {
			case errors.Is(err, store.ErrConflict):
				errs = []booking.Error{{Field: "time", Message: "Någon hann före – tiden är redan bokad. Välj en annan tid."}}
			case err != nil:
				s.log.Error("create booking", "resource", res.ID, "err", err)
				errs = []booking.Error{{Message: "Bokningen kunde inte sparas. Försök igen."}}
			default:
				if form.Remember {
					s.guard.RememberIdentity(w, auth.Identity{
						Name: form.Name, Apartment: form.Apartment,
						MMUsername: who.Username, MMUserID: who.ID,
						Phone: form.Phone,
					})
				}
				s.log.Info("booking created", "id", b.ID, "resource", res.ID,
					"start", b.Start, "end", b.End, "member", b.MMUsername)
				go s.notifyCreated(b, res)
				http.Redirect(w, r, "/bokning/"+b.ID+"?ny=1", http.StatusSeeOther)
				return
			}
		}
	}

	// Something was wrong: re-render the page with the errors and the form data.
	v.Title = res.Name
	data := map[string]any{"Resource": res, "Rules": summarize(res), "Errors": errs, "Form": form}
	switch res.Rules.Mode {
	case config.ModeHours:
		// buildHourPage reads the posted fields, so the member's day, length
		// and start time survive the round trip and the form stays open.
		page, err := s.buildHourPage(r, res, now, loc, form.Member)
		if err == nil {
			data["Hours"] = page
		}
		v.Data = data
		s.render(w, r, http.StatusUnprocessableEntity, "resource_hours.html", v)
	case config.ModeDays:
		page, err := s.buildDayPage(r, res, now, loc, form.Member)
		if err == nil {
			data["Days"] = page
		}
		v.Data = data
		s.render(w, r, http.StatusUnprocessableEntity, "resource_days.html", v)
	}
}

// parseInterval turns the posted fields into a concrete time interval.
func (s *Server) parseInterval(res config.Resource, r *http.Request, loc *time.Location) (time.Time, time.Time, []booking.Error) {
	switch res.Rules.Mode {
	case config.ModeHours:
		date := r.FormValue("datum")
		clock := r.FormValue("start")
		dur, err := booking.ParseHours(r.FormValue("langd"))
		if err != nil {
			return time.Time{}, time.Time{}, []booking.Error{{Field: "duration", Message: "Välj hur länge du vill boka."}}
		}
		start, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, loc)
		if err != nil {
			return time.Time{}, time.Time{}, []booking.Error{{Field: "time", Message: "Välj en starttid."}}
		}
		return start, start.Add(dur), nil
	case config.ModeDays:
		from, errA := time.ParseInLocation("2006-01-02", r.FormValue("fran"), loc)
		to, errB := time.ParseInLocation("2006-01-02", r.FormValue("till"), loc)
		if errA != nil || errB != nil {
			return time.Time{}, time.Time{}, []booking.Error{{Field: "time", Message: "Välj datum för in- och utcheckning."}}
		}
		start, end := booking.DayRange(res, from, to, loc)
		return start, end, nil
	}
	return time.Time{}, time.Time{}, []booking.Error{{Message: "Okänd bokningstyp."}}
}

func (s *Server) handleBooking(w http.ResponseWriter, r *http.Request, v *view) {
	b, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Bokningen hittades inte",
			"Länken kan vara gammal, eller bokningen borttagen.")
		return
	}
	res, known := s.cfg.Resource(b.ResourceID)
	if !known {
		res = config.Resource{ID: b.ResourceID, Name: b.ResourceID}
	}
	loc := s.cfg.Location()
	ev := s.event(b, res)

	rows := s.rows([]store.Booking{b}, loc)
	v.Title = "Bokning – " + res.Name
	v.Data = map[string]any{
		"Row":         rows[0],
		"New":         r.URL.Query().Get("ny") == "1",
		"Google":      ical.GoogleLink(ev),
		"Outlook":     ical.OutlookLink(ev),
		"ICS":         "/bokning/" + b.ID + "/kalender.ics",
		"CancelToken": b.CancelToken,
		"Mine":        b.MMUsername != "" && b.MMUsername == store.Member(v.Ident.MMUsername),
	}
	s.render(w, r, http.StatusOK, "booking.html", v)
}

func (s *Server) handleBookingICS(w http.ResponseWriter, r *http.Request, v *view) {
	b, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	res, ok := s.cfg.Resource(b.ResourceID)
	if !ok {
		res = config.Resource{ID: b.ResourceID, Name: b.ResourceID}
	}
	data := ical.Calendar(res.Name, []ical.Event{s.event(b, res)})
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+
		ical.Filename(res.ID, b.Start.In(s.cfg.Location()))+"\"")
	w.Write(data)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, v *view) {
	id := r.PathValue("id")
	b, err := s.store.Get(r.Context(), id)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Bokningen hittades inte", "Den kan redan vara avbokad.")
		return
	}
	token := r.FormValue("token")
	// A member may cancel with the token from their confirmation, or because
	// the booking belongs to the Mattermost account this browser remembers.
	own := v.Ident.MMUsername != "" && store.Member(v.Ident.MMUsername) == b.MMUsername
	if token != b.CancelToken && !own && !v.Role.Admin() {
		s.renderError(w, r, http.StatusForbidden, "Kunde inte avboka",
			"Använd länken i bekräftelsen från boten i Mattermost, eller be husets administratör om hjälp.")
		return
	}
	if err := s.store.Cancel(r.Context(), id, b.CancelToken, true, s.now()); err != nil {
		s.renderError(w, r, http.StatusConflict, "Kunde inte avboka", "Bokningen är kanske redan avbokad.")
		return
	}
	res, ok := s.cfg.Resource(b.ResourceID)
	if !ok {
		res = config.Resource{ID: b.ResourceID, Name: b.ResourceID}
	}
	b.Status = store.StatusCancelled
	s.log.Info("booking cancelled", "id", b.ID, "resource", b.ResourceID)
	go s.notifyCancelled(b, res)
	http.Redirect(w, r, "/mina?avbokad="+id, http.StatusSeeOther)
}

func (s *Server) handleMyBookings(w http.ResponseWriter, r *http.Request, v *view) {
	loc := s.cfg.Location()

	// Whose bookings? The remembered account by default, or whoever the field
	// names. That is looked up the same way the booking form does it, so a
	// name, a nickname or half a name finds the person rather than quietly
	// finding nobody.
	member := store.Member(v.Ident.MMUsername)
	name, typed, lookupErr := v.Ident.Name, member, ""
	if q := strings.TrimSpace(r.URL.Query().Get("medlem")); q != "" {
		typed = q
		who, errs := s.findMember(r.Context(), q, false)
		if len(errs) > 0 {
			member, lookupErr = "", errs[0].Message
		} else {
			member, name, typed = store.Member(who.Username), who.DisplayName(), who.Username
		}
	}

	var rows []bookingRow
	if member != "" {
		list, err := s.store.ByMember(r.Context(), member, false, s.now().AddDate(0, 0, -30))
		if err != nil {
			s.log.Error("load own bookings", "err", err)
		} else {
			rows = s.rows(list, loc)
		}
	}
	v.Title = "Mina bokningar"
	v.Data = map[string]any{
		"Rows":      rows,
		"Member":    member,
		"Name":      name,
		"Typed":     typed,
		"Error":     lookupErr,
		"Cancelled": r.URL.Query().Get("avbokad") != "",
	}
	s.render(w, r, http.StatusOK, "mine.html", v)
}

// handleResourceFeed publishes a resource's upcoming bookings as an .ics feed,
// so the house calendar can subscribe to it.
func (s *Server) handleResourceFeed(w http.ResponseWriter, r *http.Request, v *view) {
	res, ok := s.cfg.Resource(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	now := s.now()
	list, err := s.store.InRange(r.Context(), res.ID, now.AddDate(0, 0, -90), now.AddDate(1, 0, 0))
	if err != nil {
		http.Error(w, "kunde inte läsa bokningar", http.StatusInternalServerError)
		return
	}
	events := make([]ical.Event, 0, len(list))
	for _, b := range list {
		events = append(events, s.event(b, res))
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Write(ical.Calendar(s.cfg.Site.Title+" – "+res.Name, events))
}

// event renders a booking as a calendar event.
func (s *Server) event(b store.Booking, res config.Resource) ical.Event {
	desc := "Bokad av " + b.Name
	if b.Apartment != "" {
		desc += " (lgh " + b.Apartment + ")"
	}
	if b.Note != "" {
		desc += "\n" + b.Note
	}
	if res.Instructions != "" {
		desc += "\n\n" + res.Instructions
	}
	desc += "\n\nAvboka: " + s.rt.BaseURL + "/bokning/" + b.ID
	return ical.Event{
		UID:         b.ID + "@booking.rudbeckia.nu",
		Summary:     res.Name + " – " + b.Name,
		Description: desc,
		Location:    res.Location,
		Start:       b.Start,
		End:         b.End,
		Created:     b.CreatedAt,
		URL:         s.rt.BaseURL + "/bokning/" + b.ID,
		Cancelled:   b.Status == store.StatusCancelled,
	}
}
