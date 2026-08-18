package web

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/mikaelo/booking.rudbeckia.nu/internal/config"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/ical"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/mail"
	"github.com/mikaelo/booking.rudbeckia.nu/internal/store"
)

// notifyCreated sends the confirmation with an .ics attachment and links that
// drop the booking straight into Google or Apple Calendar.
func (s *Server) notifyCreated(b store.Booking, res config.Resource) {
	loc := s.cfg.Location()
	ev := s.event(b, res)
	link := s.rt.BaseURL + "/bokning/" + b.ID
	when := Interval(b.Start.In(loc), b.End.In(loc))

	var text bytes.Buffer
	fmt.Fprintf(&text, "Hej %s!\n\n", firstName(b.Name))
	fmt.Fprintf(&text, "Din bokning av %s är bekräftad.\n\n", res.Name)
	fmt.Fprintf(&text, "  När:  %s\n", TitleCase(when))
	if b.Mode == string(config.ModeDays) {
		fmt.Fprintf(&text, "  Längd: %s\n", Nights(nightsBetween(b, loc)))
	} else {
		fmt.Fprintf(&text, "  Längd: %s\n", durationLabel(b))
	}
	if res.Location != "" {
		fmt.Fprintf(&text, "  Var:  %s\n", res.Location)
	}
	if b.Note != "" {
		fmt.Fprintf(&text, "  Meddelande: %s\n", b.Note)
	}
	text.WriteString("\n")
	if res.Instructions != "" {
		fmt.Fprintf(&text, "%s\n\n", res.Instructions)
	}
	text.WriteString("Lägg in i kalendern\n")
	text.WriteString("  Apple Calendar, Outlook, Thunderbird: öppna den bifogade filen.\n")
	fmt.Fprintf(&text, "  Google Calendar: %s\n\n", ical.GoogleLink(ev))
	fmt.Fprintf(&text, "Se eller avboka: %s\n\n", link)
	fmt.Fprintf(&text, "Hälsningar,\n%s\n", s.cfg.Site.Title)

	var body bytes.Buffer
	fmt.Fprintf(&body, `<p>Hej %s!</p>
<p>Din bokning av <strong>%s</strong> är bekräftad.</p>
<table cellpadding="0" cellspacing="0" style="border-collapse:collapse;font-size:15px">
<tr><td style="padding:2px 16px 2px 0;color:#6f6e69">När</td><td style="padding:2px 0"><strong>%s</strong></td></tr>`,
		html.EscapeString(firstName(b.Name)), html.EscapeString(res.Name), html.EscapeString(TitleCase(when)))
	if res.Location != "" {
		fmt.Fprintf(&body, `<tr><td style="padding:2px 16px 2px 0;color:#6f6e69">Var</td><td style="padding:2px 0">%s</td></tr>`,
			html.EscapeString(res.Location))
	}
	if b.Note != "" {
		fmt.Fprintf(&body, `<tr><td style="padding:2px 16px 2px 0;color:#6f6e69">Meddelande</td><td style="padding:2px 0">%s</td></tr>`,
			html.EscapeString(b.Note))
	}
	body.WriteString("</table>")
	if res.Instructions != "" {
		fmt.Fprintf(&body, `<p style="background:#f2f0e5;padding:12px 16px;border-radius:6px">%s</p>`,
			html.EscapeString(res.Instructions))
	}
	fmt.Fprintf(&body, `<p style="margin-top:24px"><strong>Lägg in i din kalender</strong><br>
<a href="%s" style="color:#8e6b01">Google Calendar</a> &nbsp;·&nbsp;
<a href="%s" style="color:#8e6b01">Outlook</a> &nbsp;·&nbsp;
Apple Calendar och Thunderbird: öppna den bifogade filen.</p>`,
		ical.GoogleLink(ev), ical.OutlookLink(ev))
	fmt.Fprintf(&body, `<p><a href="%s" style="display:inline-block;background:#ad8301;color:#fffcf0;padding:10px 18px;border-radius:6px;text-decoration:none">Se eller avboka bokningen</a></p>`, link)
	fmt.Fprintf(&body, `<p style="color:#6f6e69;font-size:13px">%s</p>`, html.EscapeString(s.cfg.Site.Title))

	msg := mail.Message{
		To:      []string{b.Email},
		Subject: fmt.Sprintf("Bokat: %s, %s", res.Name, when),
		Text:    text.String(),
		HTML:    wrapHTML(s.cfg.Site.Title, body.String()),
		Attachments: []mail.Attachment{{
			Filename:    ical.Filename(res.ID, b.Start.In(loc)),
			ContentType: "text/calendar; charset=utf-8; method=PUBLISH",
			Data:        ical.Calendar(res.Name, []ical.Event{ev}),
		}},
	}
	if err := s.mailer.Send(msg); err != nil {
		s.log.Error("send confirmation", "booking", b.ID, "err", err)
	}
}

// notifyCancelled tells the member their booking is gone and hands their
// calendar a cancellation event.
func (s *Server) notifyCancelled(b store.Booking, res config.Resource) {
	loc := s.cfg.Location()
	when := Interval(b.Start.In(loc), b.End.In(loc))
	ev := s.event(b, res)
	ev.Cancelled = true

	text := fmt.Sprintf("Hej %s!\n\nDin bokning av %s %s är avbokad.\n\nTiden är nu ledig för någon annan i huset.\n\nHälsningar,\n%s\n",
		firstName(b.Name), res.Name, when, s.cfg.Site.Title)
	body := fmt.Sprintf(`<p>Hej %s!</p><p>Din bokning av <strong>%s</strong> %s är <strong>avbokad</strong>.</p>
<p>Tiden är nu ledig för någon annan i huset.</p>
<p><a href="%s" style="color:#8e6b01">Boka en ny tid</a></p>`,
		html.EscapeString(firstName(b.Name)), html.EscapeString(res.Name),
		html.EscapeString(when), s.rt.BaseURL+"/resurs/"+res.ID)

	msg := mail.Message{
		To:      []string{b.Email},
		Subject: fmt.Sprintf("Avbokat: %s, %s", res.Name, when),
		Text:    text,
		HTML:    wrapHTML(s.cfg.Site.Title, body),
		Attachments: []mail.Attachment{{
			Filename:    ical.Filename(res.ID, b.Start.In(loc)),
			ContentType: "text/calendar; charset=utf-8; method=CANCEL",
			Data:        ical.Calendar(res.Name, []ical.Event{ev}),
		}},
	}
	if err := s.mailer.Send(msg); err != nil {
		s.log.Error("send cancellation", "booking", b.ID, "err", err)
	}
}

// wrapHTML puts the message body in a plain, mail-client-safe shell.
func wrapHTML(title, body string) string {
	return `<!doctype html><html lang="sv"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title></head>
<body style="margin:0;padding:24px;background:#fffcf0;color:#100f0f;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;line-height:1.6">
<div style="max-width:560px;margin:0 auto">` + body + `</div></body></html>`
}

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
