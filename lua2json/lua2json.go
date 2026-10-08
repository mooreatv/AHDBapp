// Copyright 2019 MooreaTv moorea@ymail.com
// All Rights Reserved
//
// GPLv3 License (which means no commercial integration)
// ask if you need a different License
//
// This is the conversion of my lua2json.sh:
// Crude Lua table (wow saved variables) to Json "converter"
//
// go run lua2json < "C:\Program Files (x86)\World of Warcraft\_classic_\WTF\Account\$ACCT\SavedVariables\AuctionDB.lua" > auctiondb.json
//
// If you don't like regular expressions, don't look further :)
//
// In order, sed expressions:
// Add "" around toplevel array names
// Remove -- comments
// Change = to :
// Change ["foo"] to "foo"
// Change [123] to "123" (keys in json can only be strings)
// Change nil array keys to null
// Then remove trailing comas and turn lists into arrays (the awk part of the shell version; here with a stack of
// open tables so lists of lists and objects inside lists close with the right ] or }).
// NOTE: anchors/quote boundaries are important to not replace inside the middle of a string value
// Key lines (["foo"] = value, [123] = value) only get their key converted, so string values are never altered
// (e.g. a value containing " -- " or " = "), and lines that are just a value (list elements: strings, like a log line
// with "12:34:56 ...: ...", numbers including negative ones, booleans, nil) are kept as json values.

package lua2json // import "github.com/mooreatv/AHDBapp/lua2json"

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"fortio.org/log"
)

// RegsubInput is the input pattern+replacement string (or pattern).
type RegsubInput struct {
	Find      string
	ReplaceBy string
}

// Regsub is a pattern+replacement string (or pattern), with the compile regular expression.
type regsub struct {
	find      *regexp.Regexp
	replaceBy string
}

/*
	sed -E -e 's/^([^": }\t]+)/"\1"/' \
	    -e "s/ -- .*$//g" \
	    -e "s/ = /: /g" \
	    -e 's/\["/"/g' \
	    -e 's/\"]/"/g' \
	    -e 's/^([ \t]*)\[([0-9.]+)\]:/\1"\2":/' \
	    -e 's/^([ \t]*)nil,$/\1null,/'
*/
var rei = []RegsubInput{
	{` -- .*$`, ""},
	{` = `, `: `},
	{`\["`, `"`},
	{`([^\\])\"]`, `$1"`},
	{`^([ \t]*)\[([0-9.]+)\]:`, `$1"$2":`},
	{`^([ \t]*)nil,$`, `${1}null,`},
	{`^([^": {}\t0-9]+)`, `"$1"`},
}

var (
	// keyValueFind matches `["key"] = value` and `[123] = value` lines: indent, key, value.
	keyValueFind = regexp.MustCompile(`^([ \t]*)\[("(?:[^"\\]|\\.)*"|-?[0-9.]+)\] = (.*)$`)
	// keyFind matches a converted `"key": ...` line.
	keyFind = regexp.MustCompile(`^[ \t]*"(?:[^"\\]|\\.)*": `)
	// closeFind matches a table closing line.
	closeFind = regexp.MustCompile(`^[ \t]*},?$`)
	// nilElemFind matches a nil list element: indent, optional comma.
	nilElemFind = regexp.MustCompile(`^([ \t]*)nil(,?)$`)
	// valueElemFind matches a line that is only a (list element) value: string, number or boolean.
	valueElemFind = regexp.MustCompile(`^[ \t]*("(?:[^"\\]|\\.)*"|-?[0-9.]+(?:[eE][-+]?[0-9]+)?|true|false),?$`)
)

// convertLine applies the lua to json line rewrites.
func convertLine(line string, re []regsub) string {
	if m := keyValueFind.FindStringSubmatch(line); m != nil {
		key, value := m[2], m[3]
		if key[0] != '"' {
			key = `"` + key + `"` // keys in json can only be strings
		}
		switch value {
		case "nil,":
			value = "null,"
		case "nil":
			value = "null"
		}
		return m[1] + key + ": " + value
	}
	if valueElemFind.MatchString(line) {
		return line // json as is
	}
	if m := nilElemFind.FindStringSubmatch(line); m != nil {
		return m[1] + "null" + m[2]
	}
	for _, r := range re {
		line = r.find.ReplaceAllString(line, r.replaceBy)
	}
	return line
}

// changes trailing braces into trailing bracket.
func brace2bracket(line string) string {
	lastPos := len(line) - 1
	if lastPos >= 0 && line[lastPos] == '{' {
		return line[0:lastPos] + "["
	}
	return line
}

// Lua2Json stream converts a simple wow lua saved variables to json.
func Lua2Json(in io.Reader, out io.Writer, skipTop bool, bufSizeMb float64) {
	re := make([]regsub, len(rei))
	for i, r := range rei {
		re[i].find = regexp.MustCompile(r.Find)
		re[i].replaceBy = r.ReplaceBy
	}
	scanner := bufio.NewScanner(in)
	sz := int(bufSizeMb * 1024 * 1024)
	log.Infof("Using buffer size %d", sz)
	buf := make([]byte, 0, sz)
	scanner.Buffer(buf, sz)
	numLines := 0
	_, _ = out.Write([]byte("{\n"))
	// One entry per open table: 'o' object, 'a' array (list), 'u' not known yet. A table's first line decides: a key
	// line makes it an object, anything else (a value, a nested table, or nothing: empty table) an array, whose
	// opening line (still in prevLine) then gets its { turned into [. Starts with the object written above.
	stack := []byte{'o'}
	prevLine := ""
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" {
			continue // only count/process non white space/empty lines
		}
		numLines++
		if numLines == 1 && skipTop {
			continue // skip first/top level
		}
		log.Debugf("line before REs: %q", line)
		line = convertLine(line, re)
		isClose := closeFind.MatchString(line)
		top := len(stack) - 1
		log.Debugf("line after REs: %q close %v stack %q prevLine: %q", line, isClose, stack, prevLine)
		if top >= 0 && stack[top] == 'u' {
			if !isClose && keyFind.MatchString(line) {
				stack[top] = 'o'
			} else {
				stack[top] = 'a'
				prevLine = brace2bracket(prevLine)
			}
		}
		if isClose {
			// no trailing comma after the last element in json
			if lpp := len(prevLine) - 1; lpp >= 0 && prevLine[lpp] == ',' {
				prevLine = prevLine[0:lpp]
			}
			if top >= 0 {
				if stack[top] == 'a' {
					line = strings.Replace(line, "}", "]", 1)
				}
				stack = stack[:top]
			}
		}
		if lastPos := len(line) - 1; lastPos >= 0 && line[lastPos] == '{' {
			stack = append(stack, 'u')
		}
		if prevLine != "" {
			fmt.Fprintln(out, prevLine)
		}
		prevLine = line
	}
	if !skipTop {
		fmt.Fprintln(out, prevLine) // 	   END {print l}
	}
	_, _ = out.Write([]byte("}\n"))
	if err := scanner.Err(); err != nil {
		log.Errf("error scanning: %v", err)
	}
	log.Infof("Done, %d lines converted", numLines)
}
