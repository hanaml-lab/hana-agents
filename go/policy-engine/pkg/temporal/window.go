// Package temporal parses the time windows a rule can be limited to:
//
//	"Mon-Fri 09:00-17:30 Europe/Berlin"   office hours in a zone
//	"first 1h"                             the first hour after the job started
package temporal

import (
	"fmt"
	"strings"
	"time"
)

type Window struct {
	days       [7]bool
	from, to   int // minutes after midnight
	loc        *time.Location
	firstOfJob time.Duration
}

var dayIndex = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

func Parse(s string) (Window, error) {
	f := strings.Fields(s)
	if len(f) == 2 && f[0] == "first" {
		d, err := time.ParseDuration(f[1])
		if err != nil || d <= 0 {
			return Window{}, fmt.Errorf("bad window %q", s)
		}
		return Window{firstOfJob: d}, nil
	}
	if len(f) < 2 || len(f) > 3 {
		return Window{}, fmt.Errorf("window %q: want \"Mon-Fri 09:00-17:30 [Zone]\" or \"first 1h\"", s)
	}
	var w Window
	a, b, ok := strings.Cut(strings.ToLower(f[0]), "-")
	if !ok {
		b = a
	}
	da, ok1 := dayIndex[a]
	db, ok2 := dayIndex[b]
	if !ok1 || !ok2 {
		return Window{}, fmt.Errorf("window %q: bad days", s)
	}
	for d := da; ; d = (d + 1) % 7 {
		w.days[d] = true
		if d == db {
			break
		}
	}
	from, to, ok := strings.Cut(f[1], "-")
	if !ok {
		return Window{}, fmt.Errorf("window %q: bad hours", s)
	}
	var err error
	if w.from, err = minutes(from); err != nil {
		return Window{}, err
	}
	if w.to, err = minutes(to); err != nil {
		return Window{}, err
	}
	w.loc = time.UTC
	if len(f) == 3 {
		if w.loc, err = time.LoadLocation(f[2]); err != nil {
			return Window{}, err
		}
	}
	return w, nil
}

func minutes(hhmm string) (int, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, fmt.Errorf("bad time %q", hhmm)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether now falls in the window. jobStarted is used by "first" windows.
func (w Window) Contains(now, jobStarted time.Time) bool {
	if w.firstOfJob > 0 {
		return !jobStarted.IsZero() && !now.Before(jobStarted) && now.Sub(jobStarted) < w.firstOfJob
	}
	local := now.In(w.loc)
	m := local.Hour()*60 + local.Minute()
	return w.days[int(local.Weekday())] && m >= w.from && m < w.to
}
