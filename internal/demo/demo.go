// Package demo fills an empty database with plausible bookings, so that
// someone trying the site out sees a house that is already in use rather than
// an empty grid.
package demo

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/booking"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

// resident is a made-up neighbour who books things.
type resident struct {
	Name      string
	Apartment string
	Email     string
}

// The cast is fictional, and the addresses use the reserved example.com domain
// so nothing can accidentally be mailed to a real person.
var residents = []resident{
	{"Anna Andersson", "1403", "anna@example.com"},
	{"Bo Bengtsson", "0702", "bo@example.com"},
	{"Cecilia Dahl", "1201", "cecilia@example.com"},
	{"David Ek", "0304", "david@example.com"},
	{"Elin Forsberg", "0508", "elin@example.com"},
	{"Farid Hassan", "1105", "farid@example.com"},
	{"Greta Lind", "0601", "greta@example.com"},
	{"Hugo Nyström", "0907", "hugo@example.com"},
}

var bikeNotes = []string{
	"Storhandling på Ica Maxi",
	"Hämtar barnen på förskolan",
	"Ska till återvinningen",
	"Cykeltur längs Fyrisån",
	"Flyttar en bokhylla",
	"Handlar till matlaget",
	"",
	"",
}

var roomNotes = []string{
	"Mamma och pappa hälsar på",
	"Kompisar från Göteborg",
	"Syrran är i stan",
	"",
}

// Seed writes example bookings for every active resource. It is a no-op when
// the database already holds something, so restarting a demo does not pile
// bookings on top of each other.
func Seed(ctx context.Context, st *store.Store, cfg *config.Config, now time.Time) (int, error) {
	existing, err := st.Search(ctx, store.Filter{Limit: 1})
	if err != nil {
		return 0, fmt.Errorf("check for existing bookings: %w", err)
	}
	if len(existing) > 0 {
		return 0, nil
	}

	// A fixed seed keeps the demo identical every time it is started, which
	// makes it a stable thing to talk about in a README or a bug report.
	rng := rand.New(rand.NewSource(20260818))
	loc := cfg.Location()
	created := 0

	for _, res := range cfg.Resources {
		if !res.Active() {
			continue
		}
		switch res.Rules.Mode {
		case config.ModeHours:
			created += seedHours(ctx, st, res, now, loc, rng)
		case config.ModeDays:
			created += seedDays(ctx, st, res, now, loc, rng)
		}
	}
	return created, nil
}

// seedHours books a bike a few times a day for the coming week, leaving plenty
// of gaps so there is still something left to book.
func seedHours(ctx context.Context, st *store.Store, res config.Resource, now time.Time, loc *time.Location, rng *rand.Rand) int {
	openFrom, _ := config.ParseClock(res.Rules.OpenFrom)
	openTo, _ := config.ParseClock(res.Rules.OpenTo)
	step := res.Rules.SlotStepMinutes
	created := 0

	for day := 0; day < 10; day++ {
		date := now.In(loc).AddDate(0, 0, day)
		midnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)

		// Two bookings on the next few days, one after that. The point of the
		// demo is to show a resource in use while leaving plenty to book.
		count := 1
		if day < 3 {
			count = 2
		}
		for i := 0; i < count; i++ {
			hours := res.Rules.Durations[shortish(rng, len(res.Rules.Durations))]
			length := time.Duration(hours * float64(time.Hour))

			latestStart := openTo - int(length.Minutes())
			if latestStart <= openFrom {
				continue
			}
			steps := (latestStart - openFrom) / step
			start := midnight.Add(time.Duration(openFrom+rng.Intn(steps+1)*step) * time.Minute)
			end := start.Add(length)
			if start.Before(now) {
				continue
			}

			who := residents[rng.Intn(len(residents))]
			b := newBooking(res, who, start, end, bikeNotes[rng.Intn(len(bikeNotes))], now)
			buffer := time.Duration(res.Rules.BufferMinutes) * time.Minute
			// Collisions are expected when scattering random times about;
			// skipping them is simpler than placing every booking perfectly.
			if err := st.Create(ctx, b, start.Add(-buffer), end.Add(buffer)); err == nil {
				created++
			}
		}
	}
	return created
}

// seedDays books a guest room for a few stays over the coming month.
func seedDays(ctx context.Context, st *store.Store, res config.Resource, now time.Time, loc *time.Location, rng *rand.Rand) int {
	created := 0
	day := 2 + rng.Intn(5)

	for stay := 0; stay < 3; stay++ {
		nights := 2 + rng.Intn(3)
		if nights < res.Rules.MinDays {
			nights = res.Rules.MinDays
		}
		if nights > res.Rules.MaxDays {
			nights = res.Rules.MaxDays
		}
		from := now.In(loc).AddDate(0, 0, day)
		to := from.AddDate(0, 0, nights)
		start, end := booking.DayRange(res, from, to, loc)

		who := residents[rng.Intn(len(residents))]
		b := newBooking(res, who, start, end, roomNotes[rng.Intn(len(roomNotes))], now)
		if err := st.Create(ctx, b, start, end); err == nil {
			created++
		}
		// Leave a gap before the next set of guests arrives.
		day += nights + 4 + rng.Intn(8)
	}
	return created
}

// shortish picks an index biased towards the shorter durations, by drawing
// twice and keeping the lower. All-day bookings do happen, just not often.
func shortish(rng *rand.Rand, n int) int {
	a, b := rng.Intn(n), rng.Intn(n)
	if b < a {
		return b
	}
	return a
}

func newBooking(res config.Resource, who resident, start, end time.Time, note string, now time.Time) store.Booking {
	return store.Booking{
		ID:          demoID(res.ID, start),
		ResourceID:  res.ID,
		Start:       start,
		End:         end,
		Mode:        string(res.Rules.Mode),
		Name:        who.Name,
		Apartment:   who.Apartment,
		Email:       who.Email,
		Note:        note,
		Status:      store.StatusConfirmed,
		CancelToken: demoID("token-"+res.ID, start),
		CreatedAt:   now.Add(-36 * time.Hour),
		CreatedIP:   "demo",
	}
}

// demoID builds a readable, stable identifier so demo bookings are easy to
// spot in the database.
func demoID(prefix string, start time.Time) string {
	return fmt.Sprintf("demo-%s-%s", prefix, start.Format("0102-1504"))
}
