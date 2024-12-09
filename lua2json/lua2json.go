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
// Then Awk to remove trailing coma and turn list to arrays
// NOTE: anchors/quote boundaries are important to not replace inside the middle of a string value

package lua2json // import "github.com/mooreatv/AHDBapp/lua2json"

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"fortio.org/log"
)

// RegsubInput is the input pattern+replacement string (or pattern)
type RegsubInput struct {
	Find      string
	ReplaceBy string
}

// Regsub is a pattern+replacement string (or pattern), with the compile regular expression
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
	{`^([^": }\t]+)`, `"$1"`},
	{` -- .*$`, ""},
	{` = `, `: `},
	{`\["`, `"`},
	{`([^\\])\"]`, `$1"`},
	{`^([ \t]*)\[([0-9.]+)\]:`, `$1"$2":`},
	{`^([ \t]*)nil,$`, `${1}null,`},
}

// changes trailing braces into trailing bracket
func brace2bracket(line string) string {
	lastPos := len(line) - 1
	if lastPos >= 0 && line[lastPos] == '{' {
		return line[0:lastPos] + "["
	}
	return line
}

// Lua2Json stream converts a simple wow lua saved variables to json
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
	out.Write([]byte("{\n"))
	// BEGIN {startnest=0; inarray=0}
	startNest := false
	inArray := false
	trailingCommaFind := regexp.MustCompile(`},?$`)
	colonFind := regexp.MustCompile(`^[^:]+$`)
	prevLine := ""
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" {
			continue // only count/process non white space/empty lines
		}
		numLines = numLines + 1
		if numLines == 1 && skipTop {
			continue // skip first/top level
		}
		log.Debugf("line before REs: %q", line)
		for _, r := range re {
			line = r.find.ReplaceAllString(line, r.replaceBy)
		}
		log.Debugf("line after REs: %q\nin array %v nest %v prevLine: %q", line, inArray, startNest, prevLine)
		// Awk conversion section:
		//		/},?$/ {gsub(",$", "", l); if (inarray) gsub("}", "]"); inarray=0}
		if trailingCommaFind.MatchString(line) {
			lpp := len(prevLine) - 1
			if lpp >= 0 && prevLine[lpp] == ',' {
				prevLine = prevLine[0:lpp]
			}
			if inArray {
				line = strings.ReplaceAll(line, "}", "]")
			}
			inArray = false
		}
		log.Debugf("#2 prevLine: %q", prevLine)
		//		/^[^:]+$/ {if (startnest) gsub("{$", "[", l); startnest=0; inarray=1}
		if colonFind.MatchString(line) {
			if startNest {
				prevLine = brace2bracket(prevLine)
			}
			startNest = false
			inArray = true
		}
		//		/: / {inarray=0}
		if strings.Index(line, ": ") >= 0 {
			inArray = false
		}
		//		/{$/ {startnest=1}
		lastPos := len(line) - 1
		if lastPos >= 0 && line[lastPos] == '{' {
			startNest = true
			inArray = true // for empty arrays/lists
		}
		//		{if (l) print l; l=$0}
		if prevLine != "" {
			fmt.Fprintln(out, prevLine)
		}
		prevLine = line
	}
	if !skipTop {
		fmt.Fprintln(out, prevLine) // 	   END {print l}
	}
	out.Write([]byte("}\n"))
	if err := scanner.Err(); err != nil {
		log.Errf("error scanning: %v", err)
	}
	log.Infof("Done, %d lines converted", numLines)
}
