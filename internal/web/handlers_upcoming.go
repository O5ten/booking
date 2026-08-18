package web

import (
	"net/http"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
)

// dayGroup is one calendar day's worth of bookings on the upcoming list.
type dayGroup struct {
	Date  time.Time
	Rows  []bookingRow
	Today bool
}

// handleResourceUpcoming lists a resource's coming bookings in time order,
// starting from right now. Anything already finished is left out — the point
// of the page is "when is it free next", not a history.
func (s *Server) handleResourceUpcoming(w http.ResponseWriter, r *http.Request, v *view) {
	res, ok := s.cfg.Resource(r.PathValue("id"))
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "Resursen finns inte",
			"Kontrollera länken, eller gå tillbaka till startsidan.")
		return
	}
	now := s.now()
	loc := s.cfg.Location()

	// Reach a day past the booking horizon so nothing configured can fall off
	// the end of the list.
	list, err := s.store.InRange(r.Context(), res.ID, now, now.AddDate(0, 0, res.Rules.MaxAdvanceDays+1))
	if err != nil {
		s.log.Error("load upcoming", "resource", res.ID, "err", err)
		s.renderError(w, r, http.StatusInternalServerError, "Kunde inte läsa bokningarna", "Försök igen om en stund.")
		return
	}

	rows := s.rows(list, loc)
	today := truncDay(now.In(loc), loc)

	// InRange returns them in start order, so one pass groups them by day.
	var groups []dayGroup
	for _, row := range rows {
		day := truncDay(row.Start, loc)
		if n := len(groups); n > 0 && groups[n-1].Date.Equal(day) {
			groups[n-1].Rows = append(groups[n-1].Rows, row)
			continue
		}
		groups = append(groups, dayGroup{Date: day, Rows: []bookingRow{row}, Today: day.Equal(today)})
	}

	var nextFree *time.Time
	if t, ok := s.nextFree(r, res, now, loc); ok {
		nextFree = &t
	}

	v.Title = "Kommande bokningar – " + res.Name
	v.Data = map[string]any{
		"Resource": res,
		"Rules":    summarize(res),
		"Groups":   groups,
		"Count":    len(rows),
		"NextFree": nextFree,
		"Feed":     "/kalender/" + res.ID + "/flode.ics",
		"IsDays":   res.Rules.Mode == config.ModeDays,
	}
	s.render(w, r, http.StatusOK, "upcoming.html", v)
}
