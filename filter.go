package jsed

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type FilterCriteria struct {
	FullPaths   []string `json:"full_paths"`
	FullPathSep string   `json:"full_path_separator"`
	Keys        []string `json:"keys"`
}

// Match will try to match either the current key or the current path to the passed criteria
func (f *FilterCriteria) Match(breadCrumb *stringCrumb, current string) bool {
	for _, k := range f.Keys {
		if k == current {
			return true
		}
	}
	return breadCrumb.match(f)
}

const (
	enterObject = '{'
	enterList   = '['
	exitObject  = '}'
	exitList    = ']'
)

type runeCrumb []rune

func (r *runeCrumb) pop() rune {
	last := (*r)[len(*r)-1]
	*r = (*r)[:len(*r)-1]
	return last
}

func (r *runeCrumb) push(nr rune) {
	*r = append(*r, nr)
}

func (r *runeCrumb) commit(keys *stringCrumb, w io.Writer) {
	var padding []byte
	for i, rr := range *r {
		w.Write(padding)
		padding = append([]byte("  "), padding...)
		w.Write([]byte{byte(rr)})
		w.Write([]byte("\n"))
		if i <= len(*keys)-1 {
			w.Write(padding)
			w.Write([]byte("\"" + (*keys)[i] + "\""))
			w.Write([]byte(": "))
		}
	}
}

type stringCrumb []string

func (s *stringCrumb) pop() string {
	last := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return last
}

func (s *stringCrumb) push(ns string) {
	*s = append(*s, ns)
}

func (s *stringCrumb) match(criteria *FilterCriteria) bool {
NEXTCRITERIA:
	for _, c := range criteria.FullPaths {
		if len(c) != len(*s) {
			continue
		}
		for i, part := range strings.Split(c, criteria.FullPathSep) {
			if part != (*s)[i] {
				break NEXTCRITERIA
			}
		}
		return true
	}
	return false
}

type writeGate struct {
	untilLen    int
	shouldWrite bool
	writer      io.Writer
}

func (w *writeGate) write(b []byte) {
	if !w.shouldWrite {
		return
	}
	w.writer.Write(b)
}

func (w *writeGate) evalGate(crumb *runeCrumb) {
	w.shouldWrite = len(*crumb) == w.untilLen
}

type cwd int

const (
	cwdInNone cwd = iota
	cwdInObj
	cwdInArray
	cwdInTXTKey
	cwdInNumKey
	cwdInBoolKey
)

type cwdCrumb struct {
	cwd  []cwd
	last cwd
}

func (r *cwdCrumb) len() int {
	return len(r.cwd)
}

func (r *cwdCrumb) pop() cwd {
	r.last = r.cwd[len(r.cwd)-1]
	r.cwd = r.cwd[:len(r.cwd)-1]
	return r.last
}

func (r *cwdCrumb) top() cwd {
	return r.cwd[len(r.cwd)-1]
}

func (r *cwdCrumb) push(nr cwd) {
	r.cwd = append(r.cwd, nr)
}

func (r *cwdCrumb) commit(keyCrumb *stringCrumb, watermark int, w io.Writer) {
	var padding []byte
	var kIter int
	for i, rr := range r.cwd {
		if watermark != 0 && i+1 < watermark {
			switch rr {
			case cwdInObj:
				padding = append([]byte("  "), padding...)
			case cwdInArray:
				padding = append([]byte("  "), padding...)
			}
			continue
		}
		if watermark != 0 && i+1 == watermark {
			w.Write([]byte(",\n"))
		}
		w.Write(padding)
		switch rr {
		case cwdInObj:
			padding = append([]byte("  "), padding...)
			w.Write([]byte{enterObject})
			w.Write([]byte("\n"))
		case cwdInArray:
			padding = append([]byte("  "), padding...)
			w.Write([]byte{enterList})
			w.Write([]byte("\n"))
		case cwdInTXTKey:
			w.Write([]byte("\"" + (*keyCrumb)[kIter] + "\":"))
			kIter++
		case cwdInNumKey, cwdInBoolKey:
			w.Write([]byte((*keyCrumb)[kIter] + ":"))
			kIter++
		}
	}
}

type jstate struct {
	keyCrumb  *stringCrumb
	padding   int
	firstObj  bool // TODO: extend last for this
	cwd       *cwdCrumb
	writer    io.Writer
	writeOn   bool
	waterMark int
	matcher   *FilterCriteria
}

func (j *jstate) text(s string) {
	switch j.cwd.top() {
	case cwdInTXTKey, cwdInNumKey:
		j.cwd.pop() // we are in value now
		j.keyCrumb.pop()
		j.write("\"" + s + "\"")
		if j.cwd.len() < j.waterMark {
			j.writeOn = false
		}
		return
	case cwdInArray, cwdInObj:
		if !j.firstObj {
			j.comma()
		} else {
			j.firstObj = !j.firstObj
		}
	}
	j.cwd.push(cwdInTXTKey)
	j.keyCrumb.push(s)
	j.write("\"" + s + "\": ")
	if !j.writeOn {
		if j.matcher.Match(j.keyCrumb, s) {
			j.writeOn = true
			j.cwd.commit(j.keyCrumb, j.waterMark, j.writer)
			j.waterMark = j.cwd.len()
		}
	}
}

const (
	tText = "true"
	fText = "false"
)

func (j *jstate) bool(b bool) {
	s := fText
	if b {
		s = tText
	}
	switch j.cwd.top() {
	case cwdInTXTKey, cwdInNumKey:
		j.cwd.pop() // we are in value now
		j.keyCrumb.pop()
		j.write(s)
		if j.cwd.len() < j.waterMark {
			j.writeOn = false
		}
		return
	case cwdInArray, cwdInObj:
		if !j.firstObj {
			j.comma()
		} else {
			j.firstObj = !j.firstObj
		}
	}
	j.cwd.push(cwdInBoolKey)
	j.keyCrumb.push(s)
	j.write(s + ": ") // is this even valid?
	if !j.writeOn {
		if j.matcher.Match(j.keyCrumb, s) {
			j.writeOn = true
			j.cwd.commit(j.keyCrumb, j.waterMark, j.writer)
			j.waterMark = j.cwd.len()
		}
	}
}

func (j *jstate) number(s string) {
	switch j.cwd.top() {
	case cwdInTXTKey, cwdInNumKey:
		j.cwd.pop() // we are in value now
		j.keyCrumb.pop()
		j.write(s)
		return
	case cwdInArray, cwdInObj:
		if !j.firstObj {
			j.comma()
		} else {
			j.firstObj = !j.firstObj
		}
	}
	j.cwd.push(cwdInNumKey)
	j.keyCrumb.push(s)
	j.write(s + ": ")
}

func (j *jstate) pad(b []byte) {
	for i := 0; i < j.padding; i++ {
		j.writer.Write([]byte("  "))
	}
	j.writer.Write(b)
}

func (j *jstate) write(s string) {
	if !j.writeOn {
		return
	}
	j.pad([]byte(s))
}

func (j *jstate) comma() {
	j.write(", \n")
}

func (j *jstate) openObject() {
	j.firstObj = true
	if j.cwd.last == cwdInObj {
		j.comma()
	}
	j.write("{")
	j.padding++
	j.cwd.push(cwdInObj)
}

var errJSONMalformed = fmt.Errorf("json is malformed")

func (j *jstate) closeObject() {
	j.cwd.pop()
	if j.waterMark > 0 && j.cwd.len() <= j.waterMark {
		j.pad(append([]byte{exitObject}, []byte("\n")...))
		j.waterMark--
	}
	j.padding--
}

func (j *jstate) openArray() {
	j.write("[")
	j.padding++
	j.firstObj = true
	j.cwd.push(cwdInArray)
}

func (j *jstate) closeArray() {

	if j.waterMark > 0 && j.cwd.len() <= j.waterMark {
		j.pad(append([]byte{exitList}, []byte("\n")...))
		j.waterMark--
	}
	j.padding--
	j.cwd.pop()
}

func (j *jstate) begin() {}

// Filter will write the json from in into out but only the parts that match the criteria.
func Filter(in io.Reader, out io.Writer, c *FilterCriteria) error {
	state := &jstate{
		keyCrumb:  &stringCrumb{},
		padding:   0,
		firstObj:  false,
		cwd:       &cwdCrumb{},
		writer:    out,
		writeOn:   false,
		waterMark: 0,
		matcher:   c,
	}
	d := json.NewDecoder(in)
	for {

		tkn, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("retrieving next json token: %w", err)
		}
		switch val := tkn.(type) {
		case json.Delim:
			switch val { // yes this assumes correct JSON
			case enterObject:
				state.openObject()
			case enterList:
				state.openArray()
			case exitList:
				state.closeArray()
			case exitObject:
				state.closeObject()
			}

		case string:
			state.text(val)

		case json.Number:
			state.number(val.String())
		case float64:
			vval := strconv.FormatFloat(val, 'e', 10, 64)
			state.number(vval)
		case bool:
			state.bool(val)
		default:
			fmt.Printf("UNKNOWN: %v %T\n", val, val)
		}
	}
	return nil
}
