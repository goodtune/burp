package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"relTime":    relTime,
		"short":      short,
		"safeHTML":   func(v string) template.HTML { return template.HTML(v) },
		"jsonAttr":   func(v any) string { b, _ := json.Marshal(v); return string(b) },
		"jsStr":      jsStr,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"pct":        pct,
		"lower":      strings.ToLower,
		"title":      titleCase,
		"checkIcon":  checkIcon,
		"stateDot":   stateDot,
		"actionIcon": actionIcon,
		"icon":       icon,
		"sideLabel":  sideLabel,
		"plural":     plural,
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"hasPrefix": strings.HasPrefix,
		"basename":  basename,
		"dirname":   dirname,
		"dict":      dict,
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2 Jan 2006")
	}
}

// jsStr quotes a string for use inside a datastar expression.
func jsStr(v string) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b)
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return a * 100 / b
}

func titleCase(v string) string {
	v = strings.ReplaceAll(strings.ToLower(v), "_", " ")
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

// icon renders a small inline SVG glyph so that status marks share one
// visual weight regardless of platform emoji fonts.
func icon(name string) template.HTML {
	var path string
	switch name {
	case "check":
		path = `<path d="M13.5 4.5 6.5 11.5 2.5 7.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`
	case "x":
		path = `<path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>`
	case "dot":
		path = `<circle cx="8" cy="8" r="4" fill="currentColor"/>`
	case "ring":
		path = `<circle cx="8" cy="8" r="4.5" fill="none" stroke="currentColor" stroke-width="1.5"/>`
	case "dash":
		path = `<path d="M4 8h8" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>`
	case "comment":
		path = `<path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>`
	case "pencil":
		path = `<path d="M11.5 2.5l2 2-8 8H3.5v-2z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>`
	case "bang":
		path = `<path d="M8 3v6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="8" cy="12.5" r="1.2" fill="currentColor"/>`
	case "merge":
		path = `<circle cx="4" cy="3.5" r="1.5" fill="currentColor"/><circle cx="4" cy="12.5" r="1.5" fill="currentColor"/><circle cx="12" cy="8" r="1.5" fill="currentColor"/><path d="M4 5v6M4 5c0 3 4 3 6.5 3" fill="none" stroke="currentColor" stroke-width="1.5"/>`
	default:
		return ""
	}
	return template.HTML(`<svg class="i i-` + name + `" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">` + path + `</svg>`)
}

// checkIcon maps a rollup state or outcome to a glyph and css class.
func checkIcon(state string) template.HTML {
	switch strings.ToLower(state) {
	case "success":
		return `<span class="ck ck-ok" title="checks passed">` + icon("check") + `</span>`
	case "failure", "error":
		return `<span class="ck ck-bad" title="checks failed">` + icon("x") + `</span>`
	case "pending", "expected":
		return `<span class="ck ck-wait" title="checks running">` + icon("dot") + `</span>`
	case "skipped", "neutral":
		return `<span class="ck ck-skip" title="skipped">` + icon("dash") + `</span>`
	default:
		return `<span class="ck ck-none" title="no checks">` + icon("ring") + `</span>`
	}
}

// sideLabel explains which side of the diff a comment is anchored to.
func sideLabel(side string) string {
	if strings.EqualFold(side, "LEFT") {
		return "old side"
	}
	return "new side"
}

// stateDot renders the PR state marker.
func stateDot(state string, draft bool) template.HTML {
	switch {
	case state == "MERGED":
		return `<span class="dot dot-merged" title="merged"></span>`
	case state == "CLOSED":
		return `<span class="dot dot-closed" title="closed"></span>`
	case draft:
		return `<span class="dot dot-draft" title="draft"></span>`
	default:
		return `<span class="dot dot-open" title="open"></span>`
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func basename(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func dirname(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i+1]
	}
	return ""
}

// dict builds a map for passing several values to a sub-template.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict needs an even number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// actionIcon picks a glyph for the next-action card.
func actionIcon(kind string) string {
	switch kind {
	case "ready", "merged":
		return "✔"
	case "checks", "conflict", "changes", "closed":
		return "✖"
	case "draft":
		return "✎"
	case "threads":
		return "💬"
	default:
		return "●"
	}
}
