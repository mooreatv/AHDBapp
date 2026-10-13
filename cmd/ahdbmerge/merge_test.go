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

// Same case as the addon's (History.lua) merge, run in a Lua harness: same day from 2 sources.
func TestMergeDailyLikeTheAddon(t *testing.T) {
	now := int64(1000*day + 36000)
	got := MergeDaily([]Point{{now - 10*day, 50, 60, 1, 1}, {now - 5*day, 90, 100, 6, 3}},
		[]Point{{now - 5*day + 60, 70, 110, 10, 5}})
	want := []Point{{now - 10*day, 50, 60, 1, 1}, {now - 5*day + 60, 70, 110, 9, 8}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily = %v, want %v", got, want)
	}
	// same last scan: same data, kept once
	if got := MergeDaily(want, want); !reflect.DeepEqual(got, want) {
		t.Errorf("MergeDaily of the same = %v", got)
	}
}
