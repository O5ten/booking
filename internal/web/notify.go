package web

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/ical"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/mattermost"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

// notifyTimeout bounds a background notification. It is generous: the member
// is already looking at their confirmation page, nobody is waiting for this.
const notifyTimeout = 30 * time.Second

// notifyCreated direct-messages the confirmation, with the calendar file
// attached and a link that drops the booking into Google Calendar.
func (s *Server) notifyCreated(b store.Booking, res config.Resource) {
	loc := s.cfg.Location()
	ev := s.event(b, res)
	link := s.rt.BaseURL + "/bokning/" + b.ID
	when := Interval(b.Start.In(loc), b.End.In(loc))

	var m bytes.Buffer
	fmt.Fprintf(&m, "Hej %s! Din bokning av **%s** är bekräftad. :white_check_mark:\n\n", firstName(b.Name), res.Name)
	fmt.Fprintf(&m, "| | |\n|---|---|\n")
	fmt.Fprintf(&m, "| **När** | %s |\n", cell(TitleCase(when)))
	if b.Mode == string(config.ModeDays) {
		fmt.Fprintf(&m, "| **Längd** | %s |\n", Nights(nightsBetween(b, loc)))
	} else {
		fmt.Fprintf(&m, "| **Längd** | %s |\n", durationLabel(b))
	}
	if res.Location != "" {
		fmt.Fprintf(&m, "| **Var** | %s |\n", cell(res.Location))
	}
	if b.Note != "" {
		fmt.Fprintf(&m, "| **Meddelande** | %s |\n", cell(b.Note))
	}
	m.WriteString("\n")
	if res.Instructions != "" {
		fmt.Fprintf(&m, "> %s\n\n", strings.ReplaceAll(strings.TrimSpace(res.Instructions), "\n", "\n> "))
	}
	fmt.Fprintf(&m, "[Se eller avboka](%s) · [Lägg i Google Calendar](%s)\n", link, ical.GoogleLink(ev))
	m.WriteString("Kalenderfilen är bifogad – öppna den i Apple Calendar, Outlook eller Thunderbird.\n")

	s.dm(b, "bekräftelse", m.String(), mattermost.File{
		Filename: ical.Filename(res.ID, b.Start.In(loc)),
		Data:     ical.Calendar(res.Name, []ical.Event{ev}),
	})
}

// notifyCancelled tells the member their booking is gone and hands their
// calendar a cancellation event.
func (s *Server) notifyCancelled(b store.Booking, res config.Resource) {
	loc := s.cfg.Location()
	when := Interval(b.Start.In(loc), b.End.In(loc))
	ev := s.event(b, res)
	ev.Cancelled = true

	msg := fmt.Sprintf("Hej %s! Din bokning av **%s** %s är **avbokad**.\n\n"+
		"Tiden är nu ledig för någon annan i huset. [Boka en ny tid](%s)\n",
		firstName(b.Name), res.Name, cell(when), s.rt.BaseURL+"/resurs/"+res.ID)

	s.dm(b, "avbokning", msg, mattermost.File{
		Filename: ical.Filename(res.ID, b.Start.In(loc)),
		Data:     ical.Calendar(res.Name, []ical.Event{ev}),
	})
}

// dm sends one message to the member a booking belongs to. It is called from a
// goroutine, so it owns its own context and swallows nothing quietly.
func (s *Server) dm(b store.Booking, kind, message string, files ...mattermost.File) {
	if b.MMUserID == "" {
		// Bookings made while Mattermost was unconfigured have no account to
		// reach. Say so once rather than pretending the message went out.
		s.log.Warn("no mattermost account on booking, "+kind+" not sent",
			"booking", b.ID, "member", b.MMUsername)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	if err := s.mm.DM(ctx, b.MMUserID, message, files...); err != nil {
		s.log.Error("send "+kind, "booking", b.ID, "member", b.MMUsername, "err", err)
		return
	}
	s.log.Info("notified member", "booking", b.ID, "member", b.MMUsername, "kind", kind)
}

// cell escapes the pipe characters that would otherwise split a Markdown table
// cell in two. Nothing else in these messages is user-controlled markup.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func firstName(name string) string {
	if i := strings.IndexByte(name, ' '); i > 0 {
		return name[:i]
	}
	return name
}

func durationLabel(b store.Booking) string {
	return formatHours(b.End.Sub(b.Start).Hours())
}

func formatHours(h float64) string {
	if h == float64(int(h)) {
		return fmt.Sprintf("%d h", int(h))
	}
	return fmt.Sprintf("%.1f h", h)
}

// nightsBetween counts calendar nights, so a DST change cannot shift the total.
func nightsBetween(b store.Booking, loc *time.Location) int {
	s, e := b.Start.In(loc), b.End.In(loc)
	sd := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
	ed := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, loc)
	return int(ed.Sub(sd).Hours()/24 + 0.5)
}
