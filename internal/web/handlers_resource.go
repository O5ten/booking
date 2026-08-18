package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/auth"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/booking"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
)

// dayTab is one day in the horizontal date strip.
type dayTab struct {
	Date     time.Time
	Label    string
	Weekday  string
	Selected bool
	Free     bool
	Today    bool
}

// hourPage is the resource page for a bike-style resource.
type hourPage struct {
	Day       booking.DayView
	Days      []dayTab
	Durations []durationTab
	Duration  time.Duration
	// Param is the current length as it appears in links, in hours.
	Param    string
	Selected *booking.Slot

	// AllowCustom turns on the "own length" field beside the preset buttons.
	AllowCustom bool
	// IsCustom is true when the chosen length is not one of the presets, so
	// the field shows as the active choice instead of a button.
	IsCustom bool
	// CustomValue is what belongs in the field: "2" or "2,5".
	CustomValue string
	// CustomError explains a typed length that could not be used.
	CustomError string
	MinLabel    string
	MaxLabel    string
	StepLabel   string
}

type durationTab struct {
	// Param is the value for the "langd" query parameter, in hours.
	Param    string
	Label    string
	Selected bool
	Free     int
}

// dayPage is the resource page for a guest-room-style resource.
type dayPage struct {
	Month      time.Time
	Prev       time.Time
	Next       time.Time
	HasPrev    bool
	Cells      []booking.DayCell
	From       string
	To         string
	Nights     int
	RangeOK    bool
	RangeError string
	Start      time.Time
	End        time.Time
}

func (s *Server) handleResource(w http.ResponseWriter, r *http.Request, v *view) {
	res, ok := s.cfg.Resource(r.PathValue("id"))
	if !ok || !res.Active() {
		s.renderError(w, r, http.StatusNotFound, "Resursen finns inte",
			"Kontrollera länken, eller gå tillbaka till startsidan.")
		return
	}
	now := s.now()
	loc := s.cfg.Location()
	v.Title = res.Name
	data := map[string]any{
		"Resource": res,
		"Rules":    summarize(res),
		"Errors":   nil,
		"Form":     formFromIdentity(v.Ident),
	}

	switch res.Rules.Mode {
	case config.ModeHours:
		page, err := s.buildHourPage(r, res, now, loc, v.Ident.Email)
		if err != nil {
			s.log.Error("build hour page", "resource", res.ID, "err", err)
			s.renderError(w, r, http.StatusInternalServerError, "Kunde inte läsa bokningarna", "Försök igen om en stund.")
			return
		}
		data["Hours"] = page
		v.Data = data
		s.render(w, r, http.StatusOK, "resource_hours.html", v)
	case config.ModeDays:
		page, err := s.buildDayPage(r, res, now, loc, v.Ident.Email)
		if err != nil {
			s.log.Error("build day page", "resource", res.ID, "err", err)
			s.renderError(w, r, http.StatusInternalServerError, "Kunde inte läsa bokningarna", "Försök igen om en stund.")
			return
		}
		data["Days"] = page
		v.Data = data
		s.render(w, r, http.StatusOK, "resource_days.html", v)
	}
}

func (s *Server) buildHourPage(r *http.Request, res config.Resource, now time.Time, loc *time.Location, myEmail string) (*hourPage, error) {
	// r.Form covers both the query string and a posted body, so a submission
	// that fails validation re-renders the very day and slot the member chose.
	q := formValues(r)

	// Which length is the member looking at? A preset always works; anything
	// else is only honoured when the resource allows a typed-in length.
	dur := time.Duration(res.Rules.Durations[0] * float64(time.Hour))
	var customErr string
	if raw := q.Get("langd"); raw != "" {
		want, err := booking.ParseHours(raw)
		switch {
		case err != nil:
			if res.Rules.CustomDuration {
				customErr = TitleCase(err.Error()) + "."
			}
		case booking.IsPreset(res, want):
			dur = want
		case res.Rules.CustomDuration:
			if verr := booking.CheckDuration(res, want); verr != nil {
				// Keep the typed value on screen next to the complaint.
				customErr = verr.Message
			} else {
				dur = want
			}
		}
	}

	// Which day? An explicit choice always wins; otherwise open on the first
	// day that actually has something free, so nobody lands on an empty grid
	// just because today is already booked out.
	today := truncDay(now.In(loc), loc)
	day := today
	chosen := false
	if raw := q.Get("datum"); raw != "" {
		if t, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil {
			day, chosen = truncDay(t, loc), true
		}
	}
	if day.Before(today) {
		day = today
	}
	limit := today.AddDate(0, 0, res.Rules.MaxAdvanceDays)
	if day.After(limit) {
		day = limit
	}
	if !chosen {
		if first, err := s.firstFreeDay(r, res, day, limit, dur, now, loc); err != nil {
			return nil, err
		} else if !first.IsZero() {
			day = first
		}
	}

	// One day of bookings, widened so buffers on either side are visible.
	existing, err := s.store.InRange(r.Context(), res.ID,
		day.AddDate(0, 0, -1), day.AddDate(0, 0, 2))
	if err != nil {
		return nil, err
	}

	page := &hourPage{Duration: dur, Param: booking.HoursParam(dur)}
	page.Day = booking.BuildDay(res, day, dur, existing, now, loc, myEmail)

	// The date strip covers two weeks, or less if the resource does not reach.
	span := 14
	if res.Rules.MaxAdvanceDays+1 < span {
		span = res.Rules.MaxAdvanceDays + 1
	}
	stripFrom := day.AddDate(0, 0, -3)
	if stripFrom.Before(today) {
		stripFrom = today
	}
	stripBookings, err := s.store.InRange(r.Context(), res.ID,
		stripFrom.AddDate(0, 0, -1), stripFrom.AddDate(0, 0, span+1))
	if err != nil {
		return nil, err
	}
	for i := 0; i < span; i++ {
		d := stripFrom.AddDate(0, 0, i)
		if d.After(limit) {
			break
		}
		dv := booking.BuildDay(res, d, dur, stripBookings, now, loc, myEmail)
		page.Days = append(page.Days, dayTab{
			Date:     d,
			Label:    DateShort(d),
			Weekday:  WeekdayShort(d),
			Selected: ISODate(d) == ISODate(day),
			Free:     dv.FreeCount > 0,
			Today:    ISODate(d) == ISODate(today),
		})
	}

	page.AllowCustom = res.Rules.CustomDuration
	page.IsCustom = page.AllowCustom && !booking.IsPreset(res, dur)
	if page.AllowCustom {
		page.MinLabel = booking.FormatDuration(time.Duration(res.Rules.MinDurationMinutes) * time.Minute)
		page.MaxLabel = booking.FormatDuration(time.Duration(res.Rules.MaxDurationMinutes) * time.Minute)
		page.StepLabel = booking.FormatDuration(time.Duration(res.Rules.SlotStepMinutes) * time.Minute)
		page.CustomError = customErr
		switch {
		case customErr != "" && q.Get("langd") != "":
			page.CustomValue = q.Get("langd")
		case page.IsCustom:
			page.CustomValue = booking.FormatHoursInput(dur)
		}
	}

	// How many starts each length has on this day, so the buttons can show it.
	for _, d := range res.Rules.Durations {
		cand := time.Duration(d * float64(time.Hour))
		dv := booking.BuildDay(res, day, cand, existing, now, loc, myEmail)
		page.Durations = append(page.Durations, durationTab{
			Param:    booking.HoursParam(cand),
			Label:    booking.FormatDuration(cand),
			Selected: cand == dur,
			Free:     dv.FreeCount,
		})
	}

	// A chosen start time opens the confirmation form.
	if raw := q.Get("start"); raw != "" {
		for i := range page.Day.Slots {
			slot := page.Day.Slots[i]
			if Clock(slot.Start.In(loc)) == raw && slot.Available {
				page.Selected = &page.Day.Slots[i]
				break
			}
		}
	}
	return page, nil
}

func (s *Server) buildDayPage(r *http.Request, res config.Resource, now time.Time, loc *time.Location, myEmail string) (*dayPage, error) {
	q := formValues(r)
	today := truncDay(now.In(loc), loc)

	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
	if raw := q.Get("manad"); raw != "" {
		if t, err := time.ParseInLocation("2006-01", raw, loc); err == nil {
			month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		}
	}
	thisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
	if month.Before(thisMonth) {
		month = thisMonth
	}

	existing, err := s.store.InRange(r.Context(), res.ID,
		month.AddDate(0, 0, -8), month.AddDate(0, 1, 8))
	if err != nil {
		return nil, err
	}

	page := &dayPage{
		Month:   month,
		Prev:    month.AddDate(0, -1, 0),
		Next:    month.AddDate(0, 1, 0),
		HasPrev: month.After(thisMonth),
		Cells:   booking.MonthGrid(res, month, existing, now, loc, myEmail),
		From:    q.Get("fran"),
		To:      q.Get("till"),
	}

	if page.From != "" && page.To != "" {
		from, errA := time.ParseInLocation("2006-01-02", page.From, loc)
		to, errB := time.ParseInLocation("2006-01-02", page.To, loc)
		switch {
		case errA != nil || errB != nil:
			page.RangeError = "Kunde inte läsa datumen."
		case !to.After(from):
			page.RangeError = "Utcheckningen måste vara efter incheckningen."
		default:
			start, end := booking.DayRange(res, from, to, loc)
			nights := int(to.Sub(from).Hours()/24 + 0.5)
			page.Start, page.End, page.Nights = start, end, nights
			switch {
			case nights < res.Rules.MinDays:
				page.RangeError = "Kortaste bokning är " + Nights(res.Rules.MinDays) + "."
			case nights > res.Rules.MaxDays:
				page.RangeError = "Längsta bokning är " + Nights(res.Rules.MaxDays) + "."
			case start.Before(now):
				page.RangeError = "Tiden har redan passerat."
			case start.After(now.AddDate(0, 0, res.Rules.MaxAdvanceDays)):
				page.RangeError = "Du kan boka högst " + formatDays(res.Rules.MaxAdvanceDays) + " i förväg."
			default:
				conflict, err := s.store.InRange(r.Context(), res.ID,
					start.Add(-time.Duration(res.Rules.BufferMinutes)*time.Minute),
					end.Add(time.Duration(res.Rules.BufferMinutes)*time.Minute))
				if err != nil {
					return nil, err
				}
				if len(conflict) > 0 {
					page.RangeError = "Någon av nätterna är redan bokad."
				} else {
					page.RangeOK = true
				}
			}
		}
	}
	return page, nil
}

// bookingForm carries the member's details back into the template when a
// submission fails validation.
type bookingForm struct {
	Name      string
	Apartment string
	Email     string
	Phone     string
	Note      string
	Remember  bool
}

// formFromIdentity pre-fills the form from the remembered cookie.
func formFromIdentity(id auth.Identity) bookingForm {
	return bookingForm{
		Name:      id.Name,
		Apartment: id.Apartment,
		Email:     id.Email,
		Phone:     id.Phone,
		Remember:  true,
	}
}

// firstFreeDay looks ahead for the first day with a bookable start time, up to
// two weeks out. A zero time means it found nothing and the caller should keep
// the day it already had.
func (s *Server) firstFreeDay(r *http.Request, res config.Resource, from, limit time.Time, dur time.Duration, now time.Time, loc *time.Location) (time.Time, error) {
	const lookahead = 14
	to := from.AddDate(0, 0, lookahead+1)
	existing, err := s.store.InRange(r.Context(), res.ID, from.AddDate(0, 0, -1), to)
	if err != nil {
		return time.Time{}, err
	}
	for i := 0; i <= lookahead; i++ {
		day := from.AddDate(0, 0, i)
		if day.After(limit) {
			break
		}
		if booking.BuildDay(res, day, dur, existing, now, loc, "").FreeCount > 0 {
			return day, nil
		}
	}
	return time.Time{}, nil
}

// formValues returns the request's combined query and body parameters.
func formValues(r *http.Request) url.Values {
	if err := r.ParseForm(); err != nil {
		return r.URL.Query()
	}
	return r.Form
}

func truncDay(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
