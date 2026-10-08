package lua2json

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Saved variables like the WoW Forever AHDB writes: lists of strings with colons, lists of lists inside objects,
// negative numbers, booleans, strings containing " -- " and " = ", empty tables and nil list entries.
const luaInput = `AuctionDBForeverSaved = {
["log"] = {
"19:55:28 scan done in 12s: 83559 auctions",
"19:55:51 buy [Rough Bronze Leggings] for +1s 62c: pick it",
},
["settings"] = {
["flipsPos"] = {
"TOPRIGHT",
-118.8661804199219,
-85.89,
},
["debug"] = false,
["flags"] = {
true,
false,
},
},
["houses"] = {
["Horde"] = {
["items"] = {
[14374] = {
{
1791434619,
900,
900,
32,
},
{
1791435681,
900,
-1,
32,
},
},
[2589] = {
{
1791434619,
5,
6,
100,
},
},
},
["scans"] = {
{
1791434619,
78725,
3010,
},
},
},
},
["notes"] = "a -- not a comment, x = y",
["empty"] = {
},
["withNil"] = {
"a",
nil,
"c",
},
}
`

func TestLua2Json(t *testing.T) {
	var out bytes.Buffer
	Lua2Json(strings.NewReader(luaInput), &out, true, 1)
	var got any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	want := map[string]any{
		"log": []any{
			"19:55:28 scan done in 12s: 83559 auctions",
			"19:55:51 buy [Rough Bronze Leggings] for +1s 62c: pick it",
		},
		"settings": map[string]any{
			"flipsPos": []any{"TOPRIGHT", -118.8661804199219, -85.89},
			"debug":    false,
			"flags":    []any{true, false},
		},
		"houses": map[string]any{
			"Horde": map[string]any{
				"items": map[string]any{
					"14374": []any{
						[]any{1791434619.0, 900.0, 900.0, 32.0},
						[]any{1791435681.0, 900.0, -1.0, 32.0},
					},
					"2589": []any{[]any{1791434619.0, 5.0, 6.0, 100.0}},
				},
				"scans": []any{[]any{1791434619.0, 78725.0, 3010.0}},
			},
		},
		"notes":   "a -- not a comment, x = y",
		"empty":   []any{},
		"withNil": []any{"a", nil, "c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v\njson:\n%s", got, want, out.String())
	}
}
