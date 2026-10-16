package main

// AHDB price history (AuctionDB History.lua), per auction house:
// - items[itemID] = recent scans "ts,min,median,qty ts,min,median,qty ..." oldest first
// - daily[itemID] = one entry per older day "ts,min,median,qty,scans ..." oldest first (ts of the day's last scan)
// - scans = list of {ts, entries, items}
// Merged like the addon does with prices from trusted characters: same scan (ts) kept once, same day combined (lowest
// min, median of the entry with more scans, scan weighted quantity, scans added; but an entry that is the other one
// plus later scans replaces it, not to count scans twice), then compacted like the addon does
// (scans of days before the house's last 30 scans or the last 7 days rolled into daily entries, those older than 180
// days dropped).

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	day         = 86400
	maxScans    = 100
	recentScans = 30 // RECENT_SCANS, RECENT_DAYS and DAILY_DAYS of History.lua
	recentDays  = 7
	dailyDays   = 180
)

// Point is a recent scan {ts, min, median, qty} or a daily entry {ts, min, median, qty, scans}.
type Point []int64

// House is the price history of one auction house.
type House struct {
	Items map[int64][]Point
	Daily map[int64][]Point
	Scans []Point
}

// Houses by name ("Alliance", "Horde", "Neutral").
type Houses map[string]*House

func newHouse() *House {
	return &House{Items: map[int64][]Point{}, Daily: map[int64][]Point{}}
}

func parsePoints(s string) ([]Point, error) {
	var list []Point
	for ps := range strings.FieldsSeq(s) {
		var p Point
		for f := range strings.SplitSeq(ps, ",") {
			v, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad point %q", ps)
			}
			p = append(p, v)
		}
		list = append(list, p)
	}
	return list, nil
}

func numbers(t *Table) Point {
	var p Point
	for _, e := range t.Entries {
		if v, ok := e.Val.(float64); ok {
			p = append(p, int64(math.Round(v)))
		}
	}
	return p
}

// points reads a history value: the current string format, or a list of {ts, min, median, qty} tables (AHDB 2.0.x).
func points(v any) ([]Point, error) {
	switch v := v.(type) {
	case string:
		return parsePoints(v)
	case *Table:
		var list []Point
		for _, e := range v.Entries {
			if t, ok := e.Val.(*Table); ok {
				list = append(list, numbers(t))
			}
		}
		return list, nil
	}
	return nil, fmt.Errorf("unexpected history value %v", v)
}

func readItems(t *Table, into map[int64][]Point) error {
	for _, e := range t.Entries {
		id, ok := e.Key.(float64)
		if !ok {
			continue
		}
		list, err := points(e.Val)
		if err != nil {
			return fmt.Errorf("item %v: %w", id, err)
		}
		if len(list) > 0 {
			into[int64(id)] = list
		}
	}
	return nil
}

// ReadHouses converts the saved "houses" table.
func ReadHouses(t *Table) (Houses, error) {
	hs := Houses{}
	for _, e := range t.Entries {
		name, ok1 := e.Key.(string)
		ht, ok2 := e.Val.(*Table)
		if !ok1 || !ok2 {
			continue
		}
		h := newHouse()
		if it := ht.Get("items"); it != nil {
			if tt, ok := it.Val.(*Table); ok {
				if err := readItems(tt, h.Items); err != nil {
					return nil, fmt.Errorf("%s items: %w", name, err)
				}
			}
		}
		if d := ht.Get("daily"); d != nil {
			if tt, ok := d.Val.(*Table); ok {
				if err := readItems(tt, h.Daily); err != nil {
					return nil, fmt.Errorf("%s daily: %w", name, err)
				}
			}
		}
		if s := ht.Get("scans"); s != nil {
			if tt, ok := s.Val.(*Table); ok {
				for _, se := range tt.Entries {
					if st, ok := se.Val.(*Table); ok {
						h.Scans = append(h.Scans, numbers(st))
					}
				}
			}
		}
		hs[name] = h
	}
	return hs, nil
}

func sortByTS(list []Point) {
	sort.SliceStable(list, func(i, j int) bool { return list[i][0] < list[j][0] })
}

// mergeTS is the union by ts (first field), sorted.
func mergeTS(a, b []Point) []Point {
	seen := map[int64]bool{}
	var out []Point
	for _, list := range [][]Point{a, b} {
		for _, p := range list {
			if len(p) == 0 || seen[p[0]] {
				continue
			}
			seen[p[0]] = true
			out = append(out, p)
		}
	}
	sortByTS(out)
	return out
}

func lowest(a, b int64) int64 {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

// includes returns whether the daily entry q can be p combined with later scans: later last scan, more scans, min as
// low. Then it's taken as such (an older copy of the same day merged again, e.g. from a file written before the last
// sync), instead of counting p's scans twice.
func includes(q, p Point) bool {
	return q[0] > p[0] && q[4] > p[4] && lowest(q[1], p[1]) == q[1]
}

// MergeDaily merges daily entries by day, same rules as the addon's mergeDaily (History.lua), except an entry that
// includes the other one (see includes) is kept as is.
func MergeDaily(a, b []Point) []Point {
	byDay := map[int64]Point{}
	var days []int64
	for _, list := range [][]Point{a, b} {
		for _, p := range list {
			if len(p) < 5 {
				continue
			}
			d := p[0] / day
			q, ok := byDay[d]
			if !ok {
				byDay[d] = p
				days = append(days, d)
				continue
			}
			switch {
			case q[0] == p[0] || includes(q, p): // same last scan: same data
				continue
			case includes(p, q):
				byDay[d] = p
				continue
			}
			n := q[4] + p[4]
			med := p[2]
			if q[4] >= p[4] {
				med = q[2]
			}
			if med == 0 {
				med = max(q[2], p[2])
			}
			qty := ((q[3]*q[4]+p[3]*p[4])*2 + n) / (2 * n) // rounded like math.floor(x / n + 0.5)
			byDay[d] = Point{max(q[0], p[0]), lowest(q[1], p[1]), med, qty, n}
		}
	}
	slices.Sort(days)
	out := make([]Point, 0, len(days))
	for _, d := range days {
		out = append(out, byDay[d])
	}
	return out
}

// median is the lower median of the non-zero values, 0 if none (the addon's ADB:Median).
func median(values []int64) int64 {
	var v []int64
	for _, x := range values {
		if x > 0 {
			v = append(v, x)
		}
	}
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	return v[(len(v)+1)/2-1]
}

// rollup makes one {ts, min, median, qty, scans} per day of the scan points (sorted), like the addon's rollup.
func rollup(points []Point) []Point {
	var days []Point
	for i := 0; i < len(points); {
		d := points[i][0] / day
		var minU, qty, n, ts int64
		var meds []int64
		for ; i < len(points) && points[i][0]/day == d; i++ {
			p := points[i]
			minU = lowest(minU, p[1])
			meds = append(meds, p[2])
			qty += p[3]
			n++
			ts = p[0]
		}
		days = append(days, Point{ts, minU, median(meds), (2*qty + n) / (2 * n), n})
	}
	return days
}

// recentDay is the first day kept as single scans: the days of the house's last recentScans scans, at most recentDays
// days (today included), like the addon's recentDay.
func (h *House) recentDay(now int64) int64 {
	first := now/day - recentDays + 1
	if n := len(h.Scans); n >= recentScans && len(h.Scans[n-recentScans]) > 0 {
		first = max(first, h.Scans[n-recentScans][0]/day)
	}
	return first
}

// Compact rolls the scans of the days before the recent ones into daily entries and drops the daily entries older
// than dailyDays, like the addon does at each scan (CompactHistory), so merged history doesn't bring back what the
// addon already rolled up.
func (h *House) Compact(now int64) {
	cutoff := h.recentDay(now)
	oldest := now - dailyDays*day
	for id, list := range h.Items {
		var keep, old []Point
		for _, p := range list {
			switch {
			case len(p) < 4:
			case p[0]/day < cutoff:
				old = append(old, p)
			default:
				keep = append(keep, p)
			}
		}
		if len(keep) == len(list) {
			continue
		}
		h.Daily[id] = MergeDaily(h.Daily[id], rollup(old))
		h.Items[id] = keep
		if len(keep) == 0 {
			delete(h.Items, id)
		}
	}
	for id, list := range h.Daily {
		keep := slices.DeleteFunc(slices.Clone(list), func(p Point) bool { return p[0] < oldest })
		switch {
		case len(keep) == 0:
			delete(h.Daily, id)
		case len(keep) < len(list):
			h.Daily[id] = keep
		}
	}
}

// Compact compacts every house.
func (hs Houses) Compact(now int64) {
	for _, h := range hs {
		h.Compact(now)
	}
}

// Merge adds the history of other into h.
func (h *House) Merge(other *House) {
	for id, list := range other.Items {
		h.Items[id] = mergeTS(h.Items[id], list)
	}
	for id, list := range other.Daily {
		h.Daily[id] = MergeDaily(h.Daily[id], list)
	}
	h.Scans = mergeTS(h.Scans, other.Scans)
	if len(h.Scans) > maxScans {
		h.Scans = h.Scans[len(h.Scans)-maxScans:]
	}
}

// Merge adds the history of other into hs.
func (hs Houses) Merge(other Houses) {
	for name, oh := range other {
		h := hs[name]
		if h == nil {
			h = newHouse()
			hs[name] = h
		}
		h.Merge(oh)
	}
}

// Counts returns items, recent points, daily entries, scans.
func (h *House) Counts() (items, recent, daily, scans int) {
	ids := map[int64]bool{}
	for id, l := range h.Items {
		ids[id] = true
		recent += len(l)
	}
	for id, l := range h.Daily {
		ids[id] = true
		daily += len(l)
	}
	return len(ids), recent, daily, len(h.Scans)
}

// Added describes what merged (compacted at now) has more than hs, per auction house, e.g. "Horde AH: +3 items, +40
// recent scan prices, +2 daily, +1 scans"; "" if it's only hs compacted. hs is compared compacted at now too, so the
// scans rolled into daily entries since it was last compacted don't count as removed scans and added days.
func (hs Houses) Added(merged Houses, now int64) string {
	base := Houses{}
	base.Merge(hs)
	base.Compact(now)
	if base.Lua("\n") == merged.Lua("\n") {
		return ""
	}
	hs = base
	var parts []string
	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var items, recent, daily, scans int
		if h := hs[n]; h != nil {
			items, recent, daily, scans = h.Counts()
		}
		mi, mr, md, ms := merged[n].Counts()
		if mi == items && mr == recent && md == daily && ms == scans {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s AH: %+d items, %+d recent scan prices, %+d daily, %+d scans", n,
			mi-items, mr-recent, md-daily, ms-scans))
	}
	if len(parts) == 0 {
		return "same days combined"
	}
	return strings.Join(parts, "; ")
}

func joinPoints(list []Point) string {
	parts := make([]string, len(list))
	for i, p := range list {
		f := make([]string, len(p))
		for j, v := range p {
			f[j] = strconv.FormatInt(v, 10)
		}
		parts[i] = strings.Join(f, ",")
	}
	return strings.Join(parts, " ")
}

func sortedIDs(m map[int64][]Point) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Lua writes the houses table (the value of ["houses"] in the saved variables), nl as line ending.
func (hs Houses) Lua(nl string) string {
	var sb strings.Builder
	w := func(indent int, s string) {
		sb.WriteString(strings.Repeat("\t", indent))
		sb.WriteString(s)
		sb.WriteString(nl)
	}
	sb.WriteString("{" + nl)
	names := make([]string, 0, len(hs))
	for n := range hs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := hs[n]
		w(2, fmt.Sprintf("[%q] = {", n))
		for _, part := range []struct {
			key string
			m   map[int64][]Point
		}{{"items", h.Items}, {"daily", h.Daily}} {
			w(3, fmt.Sprintf("[%q] = {", part.key))
			for _, id := range sortedIDs(part.m) {
				if len(part.m[id]) > 0 {
					w(4, fmt.Sprintf("[%d] = %q,", id, joinPoints(part.m[id])))
				}
			}
			w(3, "},")
		}
		w(3, `["scans"] = {`)
		for _, s := range h.Scans {
			f := make([]string, len(s))
			for j, v := range s {
				f[j] = strconv.FormatInt(v, 10)
			}
			w(4, "{"+strings.Join(f, ", ")+"},")
		}
		w(3, "},")
		w(2, "},")
	}
	sb.WriteString("\t}")
	return sb.String()
}
