package web

import (
	"fmt"
	"strings"
	"time"
)

var svWeekdays = [...]string{"söndag", "måndag", "tisdag", "onsdag", "torsdag", "fredag", "lördag"}
var svWeekdaysShort = [...]string{"sön", "mån", "tis", "ons", "tor", "fre", "lör"}
var svMonths = [...]string{"januari", "februari", "mars", "april", "maj", "juni",
	"juli", "augusti", "september", "oktober", "november", "december"}
var svMonthsShort = [...]string{"jan", "feb", "mar", "apr", "maj", "jun",
	"jul", "aug", "sep", "okt", "nov", "dec"}

// Weekday returns "måndag".
func Weekday(t time.Time) string { return svWeekdays[int(t.Weekday())] }

// WeekdayShort returns "mån".
func WeekdayShort(t time.Time) string { return svWeekdaysShort[int(t.Weekday())] }

// Month returns "augusti".
func Month(t time.Time) string { return svMonths[int(t.Month())-1] }

// MonthShort returns "aug".
func MonthShort(t time.Time) string { return svMonthsShort[int(t.Month())-1] }

// DateLong renders "måndag 18 augusti".
func DateLong(t time.Time) string {
	return fmt.Sprintf("%s %d %s", Weekday(t), t.Day(), Month(t))
}

// DateLongYear renders "måndag 18 augusti 2026".
func DateLongYear(t time.Time) string {
	return fmt.Sprintf("%s %d %s %d", Weekday(t), t.Day(), Month(t), t.Year())
}

// DateShort renders "18 aug".
func DateShort(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), MonthShort(t))
}

// MonthYear renders "augusti 2026".
func MonthYear(t time.Time) string {
	return fmt.Sprintf("%s %d", Month(t), t.Year())
}

// Clock renders "13:00".
func Clock(t time.Time) string { return t.Format("15:04") }

// ISODate renders "2026-08-18".
func ISODate(t time.Time) string { return t.Format("2006-01-02") }

// Interval renders a booking interval as compactly as it can:
// "måndag 18 augusti 13:00–15:00" or "18 aug 15:00 – 21 aug 12:00".
func Interval(start, end time.Time) string {
	if ISODate(start) == ISODate(end) {
		return fmt.Sprintf("%s %s–%s", DateLong(start), Clock(start), Clock(end))
	}
	return fmt.Sprintf("%s %s – %s %s", DateShort(start), Clock(start), DateShort(end), Clock(end))
}

// RelativeDay returns "idag", "imorgon" or the weekday name.
func RelativeDay(t, now time.Time) string {
	d := daysBetween(now, t)
	switch d {
	case 0:
		return "idag"
	case 1:
		return "imorgon"
	case 2:
		return "i övermorgon"
	}
	return Weekday(t)
}

func daysBetween(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	return int(b.Sub(a).Hours()/24 + 0.5)
}

// Nights renders a night count in Swedish.
func Nights(n int) string {
	if n == 1 {
		return "1 natt"
	}
	return fmt.Sprintf("%d nätter", n)
}

// TitleCase upper-cases the first rune, for sentence starts.
func TitleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
