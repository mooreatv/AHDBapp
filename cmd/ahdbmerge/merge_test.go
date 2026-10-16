package main

import (
	"reflect"
	"strings"
	"testing"
)

// As WoW writes them (tabs, "-- [n]" comments), with other data around the houses.
const accountA = `
AuctionDBForeverSaved = {
	["settings"] = {
		["debug"] = false,
		["keepScans"] = 3,
	},
	["log"] = {
		"12:00:01 a line with \"quotes\", -- not a comment = {", -- [1]
		"back\\slash }", -- [2]
	},
	["houses"] = {
		["Alliance"] = {
			["items"] = {
				[2589] = "86434200,100,120,5 86436000,110,130,4",
				[4306] = "86436000,500,600,20",
			},
			["daily"] = {
				[2589] = "85572000,50,60,1,1 86004000,90,100,6,3",
			},
			["scans"] = {
				{
					86434200, -- [1]
					85000, -- [2]
					7000, -- [3]
				}, -- [1]
				{
					86436000, -- [1]
					85100, -- [2]
					7001, -- [3]
				}, -- [2]
			},
		},
	},
	["lastHouse"] = "Alliance",
}
`

// Older AHDB (2.0.x): history as tables, no daily.
const accountB = `
AuctionDBForeverSaved = {
	["houses"] = {
		["Alliance"] = {
			["items"] = {
				[2589] = {
					{
						86435100, -- [1]
						95, -- [2]
						125, -- [3]
						6, -- [4]
					}, -- [1]
					{
						86436000, -- [1]
						110, -- [2]
						130, -- [3]
						4, -- [4]
					}, -- [2]
				},
			},
			["scans"] = {
				{
					86435100, -- [1]
					84000, -- [2]
					6900, -- [3]
				}, -- [1]
			},
		},
		["Horde"] = {
			["items"] = {
				[2589] = "86436100,200,210,3",
			},
			["daily"] = {
			},
			["scans"] = {
			},
		},
	},
}
`

func read(t *testing.T, text string) (Houses, *Entry) {
	t.Helper()
	tab, err := ParseAssignment(text, savedName)
	if err != nil {
		t.Fatal(err)
	}
	e := tab.Get("houses")
	if e == nil {
		t.Fatal("no houses")
	}
	hs, err := ReadHouses(e.Val.(*Table))
	if err != nil {
		t.Fatal(err)
	}
	return hs, e
}

func TestParseKeepsStrings(t *testing.T) {
	tab, err := ParseAssignment(accountA, savedName)
	if err != nil {
		t.Fatal(err)
	}
	log := tab.Get("log").Val.(*Table)
	if got := log.Entries[0].Val; got != `12:00:01 a line with "quotes", -- not a comment = {` {
		t.Errorf("log line = %q", got)
	}
	if got := log.Entries[1].Val; got != `back\slash }` {
		t.Errorf("log line 2 = %q", got)
	}
	if got := tab.Get("lastHouse").Val; got != "Alliance" {
		t.Errorf("lastHouse = %v", got)
	}
}

func TestMergeAndWrite(t *testing.T) {
	a, e := read(t, accountA)
	b, _ := read(t, accountB)
	a.Merge(b)
	al := a["Alliance"]
	want := []Point{{86434200, 100, 120, 5}, {86435100, 95, 125, 6}, {86436000, 110, 130, 4}}
	if !reflect.DeepEqual(al.Items[2589], want) {
		t.Errorf("merged recent = %v", al.Items[2589])
	}
	if len(al.Scans) != 3 || al.Scans[1][0] != 86435100 {
		t.Errorf("merged scans = %v", al.Scans)
	}
	if a["Horde"] == nil || len(a["Horde"].Items[2589]) != 1 {
		t.Errorf("Horde not merged in: %v", a["Horde"])
	}
	// write back: the rest of the file unchanged, the result reads back the same
	text := accountA[:e.Start] + a.Lua("\n") + accountA[e.End:]
	if !strings.Contains(text, `["lastHouse"] = "Alliance",`) || !strings.Contains(text, `"back\\slash }"`) {
		t.Errorf("rest of the file changed:\n%s", text)
	}
	again, _ := read(t, text)
	if !reflect.DeepEqual(again, a) {
		t.Errorf("read back differs:\n%v\nvs\n%v", again, a)
	}
	// merging again changes nothing
	before := a.Lua("\n")
	a.Merge(again)
	if a.Lua("\n") != before {
		t.Error("merge isn't idempotent")
	}
}

// Same rules as the addon's (History.lua) merge: same day from 2 sources.
func TestMergeDailyLikeTheAddon(t *testing.T) {
	now := int64(1000*day + 36000)
	got := MergeDaily([]Point{{now - 10*day, 50, 60, 1, 1}, {now - 5*day, 90, 100, 6, 3}},
		[]Point{{now - 5*day + 60, 95, 110, 10, 5}})
	want := []Point{{now - 10*day, 50, 60, 1, 1}, {now - 5*day + 60, 90, 110, 9, 8}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily = %v, want %v", got, want)
	}
	// same last scan: same data, kept once
	if got := MergeDaily(want, want); !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily of the same = %v", got)
	}
	// an older copy of a combined day (e.g. a file written before the last sync) doesn't add its scans again
	old := []Point{{now - 5*day, 90, 100, 6, 3}}
	if got := MergeDaily(want, old); !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily with an older copy = %v", got)
	}
	if got := MergeDaily(old, want); !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily of an older copy = %v", got)
	}
	// later with more scans but a higher min: not the same scans, combined
	other := []Point{{now - 5*day + 120, 95, 100, 6, 4}}
	if got := MergeDaily(old, other); !reflect.DeepEqual(got, []Point{{now - 5*day + 120, 90, 100, 6, 7}}) {
		t.Errorf("MergeDaily of different scans = %v", got)
	}
}

// Same as the addon's CompactHistory: scans before the last 48h rolled into daily, daily over 180 days dropped.
func TestCompact(t *testing.T) {
	now := int64(1000*day + 36000)
	d3 := now - 3*day
	h := newHouse()
	h.Items[1] = []Point{{d3, 100, 120, 4}, {d3 + 60, 0, 0, 0}, {d3 + 120, 90, 130, 5}, {now - 60, 80, 110, 2}}
	h.Items[2] = []Point{{d3, 10, 20, 3}}
	h.Daily[1] = []Point{{now - 200*day, 1, 2, 3, 1}, {now - 5*day, 50, 60, 1, 1}}
	h.Daily[2] = []Point{{now - 200*day, 1, 2, 3, 1}}
	h.Compact(now)
	if want := []Point{{now - 60, 80, 110, 2}}; !reflect.DeepEqual(h.Items[1], want) {
		t.Errorf("recent = %v", h.Items[1])
	}
	// lowest non 0 min, lower median of the non 0 medians, rounded average quantity (9/3), 3 scans
	if want := []Point{{now - 5*day, 50, 60, 1, 1}, {d3 + 120, 90, 120, 3, 3}}; !reflect.DeepEqual(h.Daily[1], want) {
		t.Errorf("daily = %v", h.Daily[1])
	}
	if _, ok := h.Items[2]; ok {
		t.Errorf("item 2 recent = %v", h.Items[2])
	}
	if want := []Point{{d3, 10, 20, 3, 1}}; !reflect.DeepEqual(h.Daily[2], want) {
		t.Errorf("item 2 daily = %v", h.Daily[2])
	}
	hs := Houses{"Horde": h}
	before := hs.Lua("\n")
	hs.Compact(now)
	if hs.Lua("\n") != before {
		t.Error("compacting again changed it")
	}
}

// Scans rolled into daily entries since the last compaction aren't reported as removed.
func TestAddedCompacted(t *testing.T) {
	now := int64(1000*day + 36000)
	d3 := now - 3*day
	h := newHouse()
	h.Items[1] = []Point{{d3, 100, 120, 4}, {d3 + 60, 90, 130, 5}, {now - 60, 80, 110, 2}}
	old := Houses{"Horde": h}
	merged := Houses{}
	merged.Merge(old)
	merged.Compact(now)
	if got := old.Added(merged, now); got != "" {
		t.Errorf("only compacted: %q", got)
	}
	o := newHouse()
	o.Items[1] = []Point{{now - 30, 70, 100, 1}}
	merged.Merge(Houses{"Horde": o})
	merged.Compact(now)
	if got, want := old.Added(merged, now), "Horde AH: +0 items, +1 recent scan prices, +0 daily, +0 scans"; got != want {
		t.Errorf("Added = %q, want %q", got, want)
	}
}

func TestWowExe(t *testing.T) {
	for line, want := range map[string]bool{
		`"WowB.exe","1","Console","1","1 K"`:       true,
		`"Wow.exe","1","Console","1","1 K"`:        true,
		`"WowClassic.exe","1","Console","1","1 K"`: true,
		`"WowUp-CF.exe","1","Console","1","1 K"`:   false,
		`"WowVoiceProxy.exe","1","Console","1",""`: false,
	} {
		if got := wowExe.MatchString(line); got != want {
			t.Errorf("%s: %v, want %v", line, got, want)
		}
	}
	for line, want := range map[string]bool{
		"/Applications/World of Warcraft/_classic_era_/World of Warcraft Classic.app/Contents/MacOS/World of Warcraft Classic": true,
		"/Applications/World of Warcraft/_retail_/World of Warcraft.app/Contents/MacOS/World of Warcraft":                     true,
		"/Applications/Battle.net.app/Contents/MacOS/Battle.net":                                                              false,
	} {
		if got := wowMac.MatchString(line); got != want {
			t.Errorf("%s: %v, want %v", line, got, want)
		}
	}
}
