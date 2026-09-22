// Package diff parses the unified patch text GitHub returns for changed
// files and lays it out for rendering in unified or split (side by side)
// form. It does not compute diffs itself: GitHub already did that.
package diff

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Kind classifies a diff line.
type Kind int

const (
	Context Kind = iota
	Add
	Del
	// NoNewline marks a "\ No newline at end of file" note.
	NoNewline
)

// Line is one line of a hunk. OldNo / NewNo are zero when the line does not
// exist on that side.
type Line struct {
	Kind  Kind
	OldNo int
	NewNo int
	Text  string
}

// Prefix returns the +, - or space marker for unified rendering.
func (l Line) Prefix() string {
	switch l.Kind {
	case Add:
		return "+"
	case Del:
		return "-"
	case NoNewline:
		return "\\"
	}
	return " "
}

// Hunk is one @@ section.
type Hunk struct {
	Header   string
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Section  string
	Lines    []Line
}

// File is a parsed patch.
type File struct {
	Hunks []Hunk
	// Truncated reports that GitHub omitted the patch (binary or too large).
	Truncated bool
}

// Row pairs left and right lines for split view. Either side may be nil.
type Row struct {
	Left  *Line
	Right *Line
}

// Parse parses a GitHub `patch` string. An empty patch yields a File with
// Truncated set, which callers render as "diff not available".
func Parse(patch string) (*File, error) {
	f := &File{}
	if strings.TrimSpace(patch) == "" {
		f.Truncated = true
		return f, nil
	}
	sc := bufio.NewScanner(strings.NewReader(patch))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var cur *Hunk
	oldNo, newNo := 0, 0
	for sc.Scan() {
		text := sc.Text()
		switch {
		case strings.HasPrefix(text, "@@"):
			h, err := parseHeader(text)
			if err != nil {
				return nil, err
			}
			f.Hunks = append(f.Hunks, h)
			cur = &f.Hunks[len(f.Hunks)-1]
			oldNo, newNo = h.OldStart, h.NewStart
		case cur == nil:
			// Preamble lines (diff --git, index, ---, +++) are ignored.
			continue
		case strings.HasPrefix(text, "+"):
			cur.Lines = append(cur.Lines, Line{Kind: Add, NewNo: newNo, Text: text[1:]})
			newNo++
		case strings.HasPrefix(text, "-"):
			cur.Lines = append(cur.Lines, Line{Kind: Del, OldNo: oldNo, Text: text[1:]})
			oldNo++
		case strings.HasPrefix(text, "\\"):
			cur.Lines = append(cur.Lines, Line{Kind: NoNewline, Text: strings.TrimSpace(text[1:])})
		default:
			t := text
			if strings.HasPrefix(t, " ") {
				t = t[1:]
			}
			cur.Lines = append(cur.Lines, Line{Kind: Context, OldNo: oldNo, NewNo: newNo, Text: t})
			oldNo++
			newNo++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading patch: %w", err)
	}
	return f, nil
}

func parseHeader(text string) (Hunk, error) {
	// @@ -a,b +c,d @@ optional section
	rest := strings.TrimPrefix(text, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		return Hunk{}, fmt.Errorf("malformed hunk header %q", text)
	}
	ranges := strings.Fields(rest[:end])
	if len(ranges) != 2 {
		return Hunk{}, fmt.Errorf("malformed hunk header %q", text)
	}
	h := Hunk{Header: text, Section: strings.TrimSpace(rest[end+2:])}
	var err error
	if h.OldStart, h.OldLines, err = parseRange(strings.TrimPrefix(ranges[0], "-")); err != nil {
		return Hunk{}, fmt.Errorf("malformed hunk header %q: %w", text, err)
	}
	if h.NewStart, h.NewLines, err = parseRange(strings.TrimPrefix(ranges[1], "+")); err != nil {
		return Hunk{}, fmt.Errorf("malformed hunk header %q: %w", text, err)
	}
	return h, nil
}

func parseRange(s string) (start, n int, err error) {
	n = 1
	parts := strings.SplitN(s, ",", 2)
	if start, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, err
	}
	if len(parts) == 2 {
		if n, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, err
		}
	}
	return start, n, nil
}

// SplitRows lays out a hunk side by side: runs of deletions are paired with
// the additions that follow them; context lines appear on both sides.
func SplitRows(h Hunk) []Row {
	rows := make([]Row, 0, len(h.Lines))
	lines := h.Lines
	for i := 0; i < len(lines); {
		switch lines[i].Kind {
		case Context, NoNewline:
			l := lines[i]
			rows = append(rows, Row{Left: &l, Right: &l})
			i++
		default:
			var dels, adds []Line
			for i < len(lines) && lines[i].Kind == Del {
				dels = append(dels, lines[i])
				i++
			}
			for i < len(lines) && lines[i].Kind == Add {
				adds = append(adds, lines[i])
				i++
			}
			if len(dels) == 0 && len(adds) == 0 {
				// A lone NoNewline between changes; handled above on next loop.
				l := lines[i]
				rows = append(rows, Row{Left: &l, Right: &l})
				i++
				continue
			}
			n := max(len(dels), len(adds))
			for j := 0; j < n; j++ {
				var r Row
				if j < len(dels) {
					r.Left = &dels[j]
				}
				if j < len(adds) {
					r.Right = &adds[j]
				}
				rows = append(rows, r)
			}
		}
	}
	return rows
}

// Stats counts additions and deletions.
func (f *File) Stats() (adds, dels int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				adds++
			case Del:
				dels++
			}
		}
	}
	return adds, dels
}
