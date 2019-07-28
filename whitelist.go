package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-intervals/timespanset"
)

const (
	whitelistPrefixFormat = "2006-01-02T"
	whitelistTimePostfix  = ":00Z"
)

// WhitelistInstance is responsible for one processing of whitelist and blacklist hours
type WhitelistInstance struct {
	// blacklist contains blacklists as passed arguments
	blacklist string

	// whitelist contains whitelists as passed arguments
	whitelist string
}

func (w *WhitelistInstance) parseArguments(t time.Time) *timespanset.Set {
	s := timespanset.Empty()
	if len(w.whitelist) == 0 {
		// If there's no whitelist, than the maximum range has to be allowed so that any blacklist
		// might be subtracted from it.
		w.processHours(s, t, "00:00 - 12:00, 12:00 - 00:00", "+")
	} else {
		w.processHours(s, t, w.whitelist, "+")
	}

	w.processHours(s, t, w.blacklist, "-")

	return s
}

// getExpiryDate calculates the expiry date of a node.
func (w *WhitelistInstance) getExpiryDate(t time.Time, drainTimeout time.Duration) (expiryDatetime time.Time) {
	s := w.parseArguments(t)

	// find best range
	var bestStart, bestEnd time.Time
	s.IntervalsBetween(t, t.Add(24*time.Hour), func(start, end time.Time) bool {
		if start.After(bestStart) {
			bestStart = start
			bestEnd = end
		}
		return true
	})

	interval := int(bestEnd.Add(-drainTimeout).Sub(bestStart))
	randomInterval := time.Duration(random.Intn(interval))
	return bestStart.Add(randomInterval)
}

// mergeTimespans merges time intervals.
func (w *WhitelistInstance) mergeTimespans(s *timespanset.Set, start time.Time, end time.Time, direction string) {
	switch direction {
	default:
		panic(fmt.Sprintf("mergeTimespans(): direction expected to be + or - but got '%v'", direction))
	case "+":
		s.Insert(start, end)
	case "-":
		subtrahend := timespanset.Empty()
		subtrahend.Insert(start, end)
		s.Sub(subtrahend)
	}
}

// processHours parses time stamps and passes them to mergeTimespans(), direction can be "+" or "-".
func (w *WhitelistInstance) processHours(s *timespanset.Set, t time.Time, input string, direction string) {
	// Time not specified, continue with no restrictions.
	if len(input) == 0 {
		return
	}

	t = t.Truncate(24 * time.Hour)
	creationPrefix := t.Format(whitelistPrefixFormat)
	creationNextDayPrefix := t.Add(24 * time.Hour).Format(whitelistPrefixFormat)

	// Split in intervals.
	input = strings.Replace(input, " ", "", -1)
	intervals := strings.Split(input, ",")
	for _, timeInterval := range intervals {
		times := strings.Split(timeInterval, "-")
		if len(times) != 2 {
			panic(fmt.Sprintf("processHours(): interval '%v' should be of the form `09:00 - 11:00[, 21:00 - 23:00[, ...]]`", timeInterval))
		}

		start, err := time.Parse(time.RFC3339, creationPrefix+times[0]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[0], err))
		}

		end, err := time.Parse(time.RFC3339, creationPrefix+times[1]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[1], err))
		}

		// If end is before start it means it contains midnight.
		if end.Before(start) {
			end = end.Add(24 * time.Hour)
		}

		w.mergeTimespans(s, start, end, direction)

		// also insert next day
		start, err = time.Parse(time.RFC3339, creationNextDayPrefix+times[0]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[0], err))
		}

		end, err = time.Parse(time.RFC3339, creationNextDayPrefix+times[1]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[1], err))
		}

		if end.Before(start) {
			end = end.Add(24 * time.Hour)
		}

		w.mergeTimespans(s, start, end, direction)
	}
}
