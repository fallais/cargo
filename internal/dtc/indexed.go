package dtc

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// formatDirective lets a data file declare its own columns, so the loader does
// not have to guess and a file cannot be silently misread.
const formatDirective = "# format:"

// Indexed is a Catalog that searches the raw CSV bytes instead of expanding
// them into a map.
//
// A full catalog is ~19k rows. Building a map costs roughly 21ms and 7.7MB on
// every run; indexing line offsets costs about 3ms and 1.8MB, and the file is
// already sorted, so lookups are a binary search over the bytes we embedded
// anyway. For a CLI that often exits without resolving a single code, that is
// the difference worth having.
//
// Records with the same code are allowed and sit adjacent: manufacturer codes
// are reused across makes with unrelated meanings, so a code can legitimately
// carry several definitions.
type Indexed struct {
	data    []byte
	starts  []int32 // offset of each record
	source  string
	hasMake bool
}

// NewIndexed prepares a catalog over sorted CSV bytes.
//
// The sort order is verified rather than assumed: a binary search over
// unsorted data does not fail, it just returns the wrong answer.
func NewIndexed(data []byte, source string) (*Indexed, error) {
	ix := &Indexed{data: data, source: source}

	var prevCode, prevMake string
	for offset := 0; offset < len(data); {
		end := bytes.IndexByte(data[offset:], '\n')
		if end < 0 {
			end = len(data) - offset
		}
		line := strings.TrimSpace(string(data[offset : offset+end]))

		switch {
		case strings.HasPrefix(line, formatDirective):
			spec := strings.TrimSpace(strings.TrimPrefix(line, formatDirective))
			switch strings.ToLower(spec) {
			case "code,description":
				ix.hasMake = false
			case "code,make,description":
				ix.hasMake = true
			default:
				return nil, fmt.Errorf("dtc: %s: unknown format %q", source, spec)
			}

		case line == "" || strings.HasPrefix(line, "#"):
			// comment or blank

		default:
			code, make, _, err := splitRecord(line, ix.hasMake)
			if err != nil {
				return nil, fmt.Errorf("dtc: %s: %w", source, err)
			}
			if _, err := Parse(code); err != nil {
				return nil, fmt.Errorf("dtc: %s: %w", source, err)
			}
			if code < prevCode || (code == prevCode && make < prevMake) {
				return nil, fmt.Errorf("dtc: %s: %q/%q is out of order after %q/%q",
					source, code, make, prevCode, prevMake)
			}
			prevCode, prevMake = code, make
			ix.starts = append(ix.starts, int32(offset))
		}
		offset += end + 1
	}

	return ix, nil
}

// splitRecord parses one line under the declared column layout.
func splitRecord(line string, hasMake bool) (code, make, description string, err error) {
	if hasMake {
		parts := strings.SplitN(line, ",", 3)
		if len(parts) != 3 {
			return "", "", "", fmt.Errorf("record %q needs code,make,description", line)
		}
		return strings.ToUpper(strings.TrimSpace(parts[0])),
			strings.TrimSpace(parts[1]),
			strings.TrimSpace(parts[2]), nil
	}

	c, d, found := strings.Cut(line, ",")
	if !found {
		return "", "", "", fmt.Errorf("record %q needs code,description", line)
	}
	return strings.ToUpper(strings.TrimSpace(c)), "", strings.TrimSpace(d), nil
}

// line returns record i as a subslice, without copying.
func (ix *Indexed) line(i int) []byte {
	start := int(ix.starts[i])
	end := bytes.IndexByte(ix.data[start:], '\n')
	if end < 0 {
		return ix.data[start:]
	}
	return ix.data[start : start+end]
}

// codeAt returns just the code field of record i. The search path runs this
// once per probe, so it stays in bytes: converting to string here would
// allocate on every comparison.
func (ix *Indexed) codeAt(i int) []byte {
	line := ix.line(i)
	if c := bytes.IndexByte(line, ','); c >= 0 {
		line = line[:c]
	}
	return bytes.TrimSpace(line)
}

// definition materialises record i.
func (ix *Indexed) definition(i int) Definition {
	code, make, description, err := splitRecord(string(ix.line(i)), ix.hasMake)
	if err != nil {
		return Definition{}
	}
	return Definition{
		Code:        code,
		Description: description,
		Source:      ix.source,
		Make:        make,
	}
}

// search returns the index of the first record for a code.
func (ix *Indexed) search(code string) (int, bool) {
	want := []byte(strings.ToUpper(strings.TrimSpace(code)))

	i := sort.Search(len(ix.starts), func(i int) bool {
		return bytes.Compare(ix.codeAt(i), want) >= 0
	})
	if i >= len(ix.starts) || !bytes.Equal(ix.codeAt(i), want) {
		return 0, false
	}
	return i, true
}

func (ix *Indexed) Lookup(code string) (Definition, bool) {
	i, ok := ix.search(code)
	if !ok {
		return Definition{}, false
	}
	return ix.definition(i), true
}

// LookupAll returns every definition for a code. Matching records are adjacent
// because the file is sorted by code first.
func (ix *Indexed) LookupAll(code string) []Definition {
	i, ok := ix.search(code)
	if !ok {
		return nil
	}

	want := ix.codeAt(i)
	var defs []Definition
	for ; i < len(ix.starts) && bytes.Equal(ix.codeAt(i), want); i++ {
		defs = append(defs, ix.definition(i))
	}
	return defs
}

// Len reports how many records the catalog holds.
func (ix *Indexed) Len() int { return len(ix.starts) }

// Makes returns the distinct marques the catalog holds definitions for, in
// sorted order. The vehicle picker uses it so the list offers only makes that
// actually change an answer.
func (ix *Indexed) Makes() []string {
	if !ix.hasMake {
		return nil
	}

	seen := make(map[string]struct{})
	for i := range ix.starts {
		_, make, _, err := splitRecord(string(ix.line(i)), true)
		if err != nil || make == "" {
			continue
		}
		seen[make] = struct{}{}
	}

	makes := make([]string, 0, len(seen))
	for m := range seen {
		makes = append(makes, m)
	}
	sort.Strings(makes)
	return makes
}
