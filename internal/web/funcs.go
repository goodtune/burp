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

// checkIcon maps a rollup state or outcome to a glyph and css class.
func checkIcon(state string) template.HTML {
	switch strings.ToLower(state) {
	case "success":
		return `<span class="ck ck-ok" title="checks passed">✔</span>`
	case "failure", "error":
		return `<span class="ck ck-bad" title="checks failed">✖</span>`
	case "pending", "expected":
		return `<span class="ck ck-wait" title="checks running">●</span>`
	case "skipped", "neutral":
		return `<span class="ck ck-skip" title="skipped">–</span>`
	default:
		return `<span class="ck ck-none" title="no checks">○</span>`
	}
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
