package booking

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

// Request is a booking attempt as it arrives from the form.
type Request struct {
	Resource  config.Resource
	Start     time.Time
	End       time.Time
	Name      string
	Apartment string
	Email     string
	Phone     string
	Note      string
}

// Error is a validation failure phrased for the member reading the page.
type Error struct {
	Field   string
	Message string
}

func (e Error) Error() string { return e.Message }

// Validate checks a request against the resource rules and the current
// contents of the database. It returns the interval that must be free,
// buffer included, so the caller can hand it straight to store.Create.
func Validate(ctx context.Context, st *store.Store, req Request, now time.Time, loc *time.Location) (blockFrom, blockTo time.Time, errs []Error) {
	r := req.Resource
	ru := r.Rules

	if !r.Active() {
		return blockFrom, blockTo, []Error{{Message: fmt.Sprintf("%s går inte att boka just nu.", r.Name)}}
	}
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, Error{Field: "name", Message: "Fyll i ditt namn."})
	}
	if !validEmail(req.Email) {
		errs = append(errs, Error{Field: "email", Message: "Fyll i en giltig e-postadress – bekräftelsen skickas dit."})
	}
	if !req.End.After(req.Start) {
		errs = append(errs, Error{Field: "time", Message: "Sluttiden måste vara efter starttiden."})
		return blockFrom, blockTo, errs
	}
	if req.Start.Before(now.Add(time.Duration(ru.MinNoticeMinutes) * time.Minute)) {
		errs = append(errs, Error{Field: "time", Message: "Tiden har redan passerat."})
	}
	if req.Start.After(now.AddDate(0, 0, ru.MaxAdvanceDays)) {
		errs = append(errs, Error{Field: "time", Message: fmt.Sprintf("Du kan boka högst %d dagar i förväg.", ru.MaxAdvanceDays)})
	}

	switch ru.Mode {
	case config.ModeHours:
		errs = append(errs, validateHours(r, req, loc)...)
	case config.ModeDays:
		errs = append(errs, validateDays(r, req, loc)...)
	}

	if len(errs) > 0 {
		return blockFrom, blockTo, errs
	}

	// Per-member caps.
	if ru.MaxActivePerUser > 0 {
		n, err := st.CountActiveForUser(ctx, r.ID, req.Email, now)
		if err != nil {
			return blockFrom, blockTo, []Error{{Message: "Kunde inte kontrollera dina bokningar. Försök igen."}}
		}
		if n >= ru.MaxActivePerUser {
			errs = append(errs, Error{Message: fmt.Sprintf(
				"Du har redan %d aktiva bokningar av %s (max %d). Avboka en först.", n, r.Name, ru.MaxActivePerUser)})
		}
	}
	if ru.MaxHoursPerWeekPerUser > 0 {
		weekFrom := req.Start.AddDate(0, 0, -7)
		weekTo := req.Start.AddDate(0, 0, 7)
		used, err := st.HoursForUserBetween(ctx, r.ID, req.Email, weekFrom, weekTo)
		if err != nil {
			return blockFrom, blockTo, []Error{{Message: "Kunde inte kontrollera dina bokningar. Försök igen."}}
		}
		want := req.End.Sub(req.Start).Hours()
		if used+want > ru.MaxHoursPerWeekPerUser {
			errs = append(errs, Error{Message: fmt.Sprintf(
				"Det skulle bli %.0f timmar på två veckor – gränsen är %.0f timmar för %s.",
				used+want, ru.MaxHoursPerWeekPerUser, r.Name)})
		}
	}
	if len(errs) > 0 {
		return blockFrom, blockTo, errs
	}

	// The interval that must be free is the booking widened by the buffer.
	buffer := time.Duration(ru.BufferMinutes) * time.Minute
	blockFrom = req.Start.Add(-buffer)
	blockTo = req.End.Add(buffer)

	// A cheap pre-check so the member gets a nice message rather than a
	// conflict error; store.Create repeats it inside a transaction.
	existing, err := st.InRange(ctx, r.ID, blockFrom, blockTo)
	if err != nil {
		return blockFrom, blockTo, []Error{{Message: "Kunde inte läsa befintliga bokningar. Försök igen."}}
	}
	if _, hit := blocked(req.Start, req.End, buffer, existing); hit {
		errs = append(errs, Error{Field: "time", Message: "Tiden hann bli bokad. Välj en annan tid."})
	}
	return blockFrom, blockTo, errs
}

func validateHours(r config.Resource, req Request, loc *time.Location) []Error {
	ru := r.Rules
	var errs []Error

	dur := req.End.Sub(req.Start)
	ok := false
	for _, d := range ru.Durations {
		if durationOf(d) == dur {
			ok = true
			break
		}
	}
	if !ok {
		errs = append(errs, Error{Field: "duration", Message: fmt.Sprintf(
			"Välj en av de tillåtna längderna: %s.", DurationList(ru.Durations))})
	}

	local := req.Start.In(loc)
	openFrom, openTo := dayWindow(r, local, loc)
	if local.Before(openFrom) || req.End.In(loc).After(openTo) {
		errs = append(errs, Error{Field: "time", Message: fmt.Sprintf(
			"%s kan bokas mellan %s och %s.", r.Name, ru.OpenFrom, ru.OpenTo)})
	}

	step := time.Duration(ru.SlotStepMinutes) * time.Minute
	if offset := local.Sub(openFrom) % step; offset != 0 {
		errs = append(errs, Error{Field: "time", Message: fmt.Sprintf(
			"Starttiden måste ligga på en %d-minutersgräns.", ru.SlotStepMinutes)})
	}
	return errs
}

func validateDays(r config.Resource, req Request, loc *time.Location) []Error {
	ru := r.Rules
	var errs []Error

	// Count nights from calendar dates so DST transitions cannot shift the total.
	s, e := req.Start.In(loc), req.End.In(loc)
	sd := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
	ed := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, loc)
	nights := int(ed.Sub(sd).Hours()/24 + 0.5)

	if nights < ru.MinDays {
		errs = append(errs, Error{Field: "time", Message: fmt.Sprintf(
			"Kortaste bokning är %s.", pluralNights(ru.MinDays))})
	}
	if nights > ru.MaxDays {
		errs = append(errs, Error{Field: "time", Message: fmt.Sprintf(
			"Längsta bokning är %s.", pluralNights(ru.MaxDays))})
	}
	return errs
}

func pluralNights(n int) string {
	if n == 1 {
		return "1 natt"
	}
	return fmt.Sprintf("%d nätter", n)
}

// DurationList renders allowed durations as "1 h, 2 h, 4 h eller 8 h".
func DurationList(ds []float64) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = FormatDuration(durationOf(d))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " eller " + parts[len(parts)-1]
	}
}

// FormatDuration renders a duration the way the house talks about it: "4 h",
// "30 min", "1 h 30 min".
func FormatDuration(d time.Duration) string {
	mins := int(d.Minutes())
	h, m := mins/60, mins%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d h %d min", h, m)
	}
}

func durationOf(hours float64) time.Duration {
	return time.Duration(hours * float64(time.Hour))
}

// Durations converts the configured hour values into durations.
func Durations(ds []float64) []time.Duration {
	out := make([]time.Duration, len(ds))
	for i, d := range ds {
		out[i] = durationOf(d)
	}
	return out
}

func validEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.LastIndex(s, "@")
	if at < 1 || at == len(s)-1 || len(s) > 254 {
		return false
	}
	domain := s[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	return !strings.ContainsAny(s, " \t\r\n,;<>")
}
