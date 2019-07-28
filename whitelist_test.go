package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-intervals/timespanset"
)

// Test data
var (
	a time.Time
	b time.Time
	c time.Time
)

func init() {
	var err error
	s := "2000-01-02T03:04:00Z"
	a, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
	s = "2000-01-02T04:04:00Z"
	b, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
	s = "2000-05-06T07:08:00Z"
	c, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
}

func TestMergeTimespans(t *testing.T) {
	var i WhitelistInstance

	s := timespanset.Empty()
	var cnt secondCounter

	// Check that basic scenario works.
	i.mergeTimespans(s, a, b, "+")
	s.IntervalsBetween(a, c, cnt.count)
	if cnt.s != 3600 {
		t.Errorf("Expected 3600 seconds, got '%v'", cnt.s)
	}
	cnt.s = 0

	// Check that merging a multi-day interval works.
	i.mergeTimespans(s, b, c, "+")
	s.IntervalsBetween(a, c, cnt.count)
	if cnt.s != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", cnt.s)
	}
	cnt.s = 0

	// Check that merging a zero interval with + doesn't change second count.
	i.mergeTimespans(s, a, a, "+")
	s.IntervalsBetween(a, c, cnt.count)
	if cnt.s != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", cnt.s)
	}
	cnt.s = 0

	// Check that merging a zero interval with - doesn't change second count.
	i.mergeTimespans(s, a, a, "-")
	s.IntervalsBetween(a, c, cnt.count)
	if cnt.s != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", cnt.s)
	}
	cnt.s = 0

	// Check that merging an interval with - works.
	i.mergeTimespans(s, b, c, "-")
	s.IntervalsBetween(a, c, cnt.count)
	if cnt.s != 3600 {
		t.Errorf("Expected 3600 seconds, got '%v'", cnt.s)
	}
	cnt.s = 0

	// Check that merging with a wrong direction panics.
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic on mergeTimespans(*)")
		}
	}()
	i.mergeTimespans(s, a, b, "*")
}

type secondCounter struct {
	s int64
}

func (c *secondCounter) count(start, end time.Time) bool {
	c.s += int64(end.Sub(start).Seconds())
	return true
}
