package main

// Minimal reader for the Lua tables WoW writes in SavedVariables files: strings, numbers, booleans, nil, nested
// tables and "--" comments. Each value remembers where it is in the text, so one part of a file can be replaced
// without touching the rest.

import (
	"fmt"
	"strconv"
	"strings"
)

// Table is a Lua table, entries in file order.
type Table struct {
	Entries []Entry
}

// Entry is one table field. Key is a string, a float64 (number keys) or nil (list items); Val is a string,
// float64, bool, nil or *Table. Start and End are the byte offsets of the value in the text.
type Entry struct {
	Key        any
	Val        any
	Start, End int
}

// Get returns the entry with that string key.
func (t *Table) Get(key string) *Entry {
	for i := range t.Entries {
		if k, ok := t.Entries[i].Key.(string); ok && k == key {
			return &t.Entries[i]
		}
	}
	return nil
}

type parser struct {
	s string
	i int
}

func (p *parser) errorf(format string, args ...any) error {
	line := strings.Count(p.s[:p.i], "\n") + 1
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

func (p *parser) skip() {
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.i++
		case strings.HasPrefix(p.s[p.i:], "--"):
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *parser) value() (any, error) {
	p.skip()
	if p.i >= len(p.s) {
		return nil, p.errorf("unexpected end of file")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.table()
	case c == '"':
		return p.str()
	case c == '-' || c == '.' || (c >= '0' && c <= '9'):
		return p.number()
	}
	for _, w := range []string{"true", "false", "nil"} {
		if strings.HasPrefix(p.s[p.i:], w) {
			p.i += len(w)
			switch w {
			case "true":
				return true, nil
			case "false":
				return false, nil
			}
			return nil, nil
		}
	}
	return nil, p.errorf("unexpected %q", p.s[p.i])
}

func (p *parser) number() (float64, error) {
	start := p.i
	for p.i < len(p.s) && strings.IndexByte("0123456789+-.eE", p.s[p.i]) >= 0 {
		p.i++
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		return 0, p.errorf("bad number %q", p.s[start:p.i])
	}
	return v, nil
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	var sb strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return sb.String(), nil
		case '\\':
			p.i++
			if p.i >= len(p.s) {
				return "", p.errorf("unfinished string")
			}
			e := p.s[p.i]
			switch {
			case e == 'n':
				sb.WriteByte('\n')
			case e == 't':
				sb.WriteByte('\t')
			case e == 'r':
				sb.WriteByte('\r')
			case e >= '0' && e <= '9':
				n := 0
				for k := 0; k < 3 && p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9'; k++ {
					n = n*10 + int(p.s[p.i]-'0')
					p.i++
				}
				sb.WriteByte(byte(n))
				continue
			default: // \" \\ and a backslash before a newline
				sb.WriteByte(e)
			}
			p.i++
		default:
			sb.WriteByte(c)
			p.i++
		}
	}
	return "", p.errorf("unfinished string")
}

func (p *parser) table() (*Table, error) {
	p.i++ // {
	t := &Table{}
	for {
		p.skip()
		if p.i >= len(p.s) {
			return nil, p.errorf("unfinished table")
		}
		if p.s[p.i] == '}' {
			p.i++
			return t, nil
		}
		var key any
		if p.s[p.i] == '[' {
			p.i++
			k, err := p.value()
			if err != nil {
				return nil, err
			}
			p.skip()
			if !strings.HasPrefix(p.s[p.i:], "]") {
				return nil, p.errorf("expected ]")
			}
			p.i++
			p.skip()
			if !strings.HasPrefix(p.s[p.i:], "=") {
				return nil, p.errorf("expected =")
			}
			p.i++
			key = k
		}
		p.skip()
		start := p.i
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		t.Entries = append(t.Entries, Entry{Key: key, Val: v, Start: start, End: p.i})
		p.skip()
		if p.i < len(p.s) && (p.s[p.i] == ',' || p.s[p.i] == ';') {
			p.i++
		}
	}
}

// ParseAssignment finds "name = {" at the start of a line in a SavedVariables file and reads that table.
func ParseAssignment(text, name string) (*Table, error) {
	idx := -1
	for off := 0; off < len(text); {
		k := strings.Index(text[off:], name)
		if k < 0 {
			break
		}
		k += off
		if k == 0 || text[k-1] == '\n' {
			idx = k
			break
		}
		off = k + len(name)
	}
	if idx < 0 {
		return nil, fmt.Errorf("no %s in the file", name)
	}
	p := &parser{s: text, i: idx + len(name)}
	p.skip()
	if !strings.HasPrefix(p.s[p.i:], "=") {
		return nil, p.errorf("expected = after %s", name)
	}
	p.i++
	p.skip()
	if !strings.HasPrefix(p.s[p.i:], "{") {
		return nil, p.errorf("expected a table for %s", name)
	}
	return p.table()
}
