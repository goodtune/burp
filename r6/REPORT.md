# UI review r6

- Head: `76670d3` (origin/claude/github-pr-code-review-app-ycei2p, checked out as `ui-review`)
- Build: `CGO_ENABLED=0 go build -o burp ./cmd/burp` OK; server restarted, `/healthz` → `ok`
- Stylesheet: `/inbox` HTML references `/static/app.css?v=ddb6cb11345c` — cache-buster confirmed. Fresh non-persistent Playwright context per shot (no userDataDir), so no stale CSS.
- Viewport 1440x900 @2x (pr-split-wide 1920x1080), dark unless named `-light`.

## Timings

| Action | Time |
|---|---|
| Split toggle → `table.diff.split` | 1398 ms (1365 ms at 1920) |
| Composer: click 45 ms, `.composer` visible | 1353 ms |
| Thread: click ground.html → `.thread` visible | 1892 ms |
| Each capture end-to-end (incl. dev-session + browser launch) | 11–14 s; settings 2.9 s |

## Console / HTTP errors

None. `.driver/console.log` is empty across all 11 captures (no console.error, pageerror, or HTTP ≥400). No errors/panics in `burp.log`.

## r5 findings

| Image | # | r5 finding | r6 status |
|---|---|---|---|
| pr-unified | 1 | Blank row 66 shorter; hunk header taller; three row heights | **improved** — all code rows incl. blank row 66 now 21px (DOM-measured); hunk header still 31px, so two heights remain |
| pr-unified | 2 | Files sidebar wraps `internal/web/templates/admin.html` with ~120px free | **remains** — wraps after `templates/` on admin/dashboard/login.html |
| pr-unified | 3 | `Threads 0` card is a 640px equal-thirds card for two words | **remains** |
| pr-split | 1 | Right pane line 68 wraps `"--force-dev-mode"` mid-literal at the hyphen | **remains** (different break point: now `"--force-dev-m` / `ode"} {`, a mid-word break) |
| pr-split | 2 | Continuation rows start under the sign column, not the code column | **remains** (line 70 both panes: `tput:\n%s"` starts under the `+/−` column) |
| pr-split | 3 | Left row 68 full-height red band with empty second line | **remains** |
| inbox | 1 | `✕` used for both closed and fail in legend | **fixed** — closed is now `⊗` (circled), fail is `✕` |
| inbox | 2 | Grey `●` status not in legend | **fixed** — legend ends with `● none` |
| inbox | 3 | #251/#248/#130 listed in both Needs review and Returned to you | **fixed** — `Returned to you` is 0; DOM scan finds no PR href in two sections |
| inbox | 4 | Section toggle glyphs tiny (~7px) | **improved** — solid `▼`/`▶` via `summary::before` at 14px, but glyph renders small (~8px visible) and low-contrast grey |
| inbox | 5 | Meta line grey ~3.5:1 | **remains** |
| pr-composer | 1 | Composer inset ~20px; accent bar and `+` on different verticals | **improved** — inset now ~10px each side; accent bar and `+` marker are within ~5px |
| pr-composer | 2 | `65` shifts left in anchor row | **remains** (~10px left of the other rows' numbers) |
| pr-composer | 3 | Two nested blues; header and hint on one baseline 800px apart | **remains** |
| pr-thread | 1 | Suggested change in UI font, no gutter/sign/colour/line numbers | **improved** — line numbers `8`/`8` and coloured `−`/`+` markers present, but code is still the proportional UI font and the rows have no red/green background |
| pr-thread | 2 | Inner box left-padded ~65px, ends at ~60% width | **remains** (starts ~10px in, but ends at ~48% of the outer box) |
| pr-thread | 3 | Thread card inset inside green rows, green slivers each side | **remains** (~10px slivers) |
| pr-thread | 4 | `Reply` alone; no apply-suggestion action | **remains** |
| pr-thread | 5 | Author line vs pill row ~35px apart; three colours in header | **remains** |
| pr-cards | 1 | Threads card causes horizontal overflow to 1574px | **fixed** — `scrollWidth` 1440; paths wrap at `/` via `<wbr>` |
| pr-cards | 2 | `Checks none` opens empty | **fixed** — "No checks reported for this commit." |
| pr-cards | 3 | Reviews markdown list loses bullets; `<details>` look like selects | **improved** — bullets render as a proper `<ul>` (8 `li`, hollow discs, inline code intact). The two `<details>` still render as full-width boxes with a `▾` at the far right |
| pr-cards | 4 | Cards `align-items: start`, Conversation bar sits under tallest | **remains** — heights 81 / 555 / 309px; Conversation bar below the Reviews card |
| pr-cards | 5 | Thread entries have no comment excerpt | **fixed** — each entry shows author + truncated excerpt + age |

## New / remaining problems per image

### pr-unified.png
1. Hunk header row is 31px vs 21px code rows (two heights in the hunk); blank row now matches.
2. Card chevrons (`Checks`, `Reviews`, `Threads`, `Conversation`) render as small grey `▸` (~7px) at the far right of a 640–1900px card; they are solid but barely visible.
3. Files sidebar wrap point for `internal/web/templates/*.html` still well before the stats column.

### pr-split.png
1. Line 68 right pane breaks inside the word `dev-mode` (`dev-m` / `ode`), which is worse for reading than the previous hyphen break; needs `overflow-wrap` with a break at whitespace/punctuation or no wrap with horizontal scroll.
2. Continuation lines (70) hang under the sign column; left row 68 still a 2-line red band with an empty second line.

### inbox.png
1. Section chevrons are solid `▼`/`▶` but small and dim grey; collapsed sections (`Waiting on reviewers`, `Drafts`, `Recently merged`) rely entirely on this glyph.
2. Legend `⊗ closed` is red and `✕ fail` is red: distinguishable by shape now, but same colour.
3. Meta line grey contrast still low.
4. No duplicate PRs across sections (verified in DOM).

### pr-composer.png
1. Composer card still ~10px inset from the diff-card edge on both sides while the anchor row runs full width.
2. `65` in the anchor row is shifted left because the `+` marker shares the cell.
3. Focus ring and card accent are two nested blues.

### pr-thread.png
1. Suggested change block: line numbers and `−`/`+` markers now present and coloured, but the code is in the proportional UI font and the rows have no red/green background, so removed/added rows are only distinguished by the marker.
2. Inner suggestion box ends at ~48% of the outer box; the empty right half reads as a layout bug.
3. Thread card inset ~10px inside the green rows (green slivers each side).
4. No apply-suggestion / add-to-review action; only `Reply`.
5. `unresolved` pill (amber) sits ~35px above the author line; `Resolve` link is blue on the right.

### pr-cards.png
1. Card chevrons on open cards are small grey `▾`; the `<details>` inside the Reviews body (`Show a summary per file`, `Comments suppressed …`) still look like select inputs with a far-right `▾`.
2. Cards do not equalise: `Checks` 81px, `Reviews` 555px, `Threads` 309px; the `Conversation` bar sits under the tallest and there is dead space under Checks and Threads.
3. Threads list now wraps and shows excerpts (good); the excerpt truncation leaves an orphan `1y ago` after the ellipsis on the same line as the truncated text.
4. Reviews bullets use hollow discs `○`, which read like unchecked items rather than list markers.
