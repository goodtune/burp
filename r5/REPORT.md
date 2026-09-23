# UI review r5

- **Head:** `cc844fe` (`origin/claude/github-pr-code-review-app-ycei2p`, "ui: fourth real-data review pass"), branch `ui-review`, built `CGO_ENABLED=0`, `/healthz` ok.
- **Session:** `burp dev-session 286798` (@goodtune). Viewport 1440x900 @2x dark unless stated.
- **Console / page errors / HTTP >= 400:** none across all 11 captures.

## Timings (in-page, after networkidle)

| Capture | Measure |
|---|---|
| pr-split (1440) | Split click → `table.diff.split` visible: 4348 ms |
| pr-split-wide (1920) | Split click → visible: 1383 ms (second load, warm cache) |
| pr-composer | line-number click 43 ms; `.composer` visible 1344 ms after click |
| pr-thread | file click → `#pr-diff .thread` visible: 881 ms |
| all | ~10–13 s wall per capture incl. browser launch + networkidle wait; settings 2.9 s |

## Capture sizes

inbox 2880x3766 · pr-unified 2880x1848 · pr-split 2880x1848 · pr-review-dialog 2880x1848 · pr-composer 2880x1850 · settings 2880x1800 · inbox-light 2880x3766 · pr-light 2880x1848 · pr-split-wide 3840x2160 · pr-thread 2880x2890 · **pr-cards 3148x3570 (page overflows the 1440 viewport horizontally, see below)**.

`thread-md.html` = `#pr-diff .thread .md` outerHTML from vitriolic #123 ground.html (2306 bytes, contains the GitHub `js-suggested-changes-blob` block).

## r4 findings status

| Image | # | r4 finding | r5 status |
|---|---|---|---|
| pr-unified | 1 | Blank row 66 ~5px shorter than neighbours | **remains** (still visibly shorter in unified and split) |
| pr-unified | 2 | Sidebar vs diff card height mismatch; legend wraps to two rows | **remains** (sidebar ends ~1140, diff ~875; legend + `shortcuts` still on two rows) |
| pr-unified | 3 | `@goodtune · requested` is the only dashed pill | **remains** |
| pr-split | 1 | Unified→Split moves code start (≈715 → ≈645) | **remains** |
| pr-split | 2 | Wrapped continuation of line 70 hangs left of its own indent | **remains** (continuation now starts at x≈609, left of the code column start ≈645, i.e. under the sign column) |
| pr-split | 3 | Row 68 left side padded with full-width empty red band | **remains** |
| pr-split | 4 | Row 66 short | **remains** |
| pr-split | 5 | Sign column wide: `−`/`+` ~15px from number, ~50px from code | **remains** |
| pr-split-wide | 1 | Right pane has no right padding, long lines touch the edge | **improved** at 1440: right-pane line 70 wraps ~50px short of the card border (wide not re-inspected) |
| pr-split-wide | 2 | Threads card gets equal width with only `Threads 0` | **remains** |
| pr-split-wide | 3 | Row 66 short | **remains** |
| inbox | 1 | Right status column has no legend/header; ✔/✕ line icons vs ● filled dot | **improved** — top-right legend now has `checks ✔ pass ✕ fail ● running`; the grey ● (no checks) is still not in the legend and the icon-style mix remains |
| inbox | 2 | `Nothing here.` misaligned with PR titles | **fixed** (now starts at the repo-name column) |
| inbox | 3 | Meta line low contrast; `re-requested after changes` not distinct | **improved** — re-requested is now an amber pill; meta grey contrast unchanged |
| inbox | 4 | Section toggles ~7px glyphs | **remains** |
| inbox | 5 | Filter placeholder and `Syntax: …` two greys, ↗ smaller | **remains** (`Syntax:` grey, link light, ↗ still smaller) |
| pr-composer | 1 | Composer separated from its anchor row by ~20px above/below | **improved** — card now sits directly under row 65 (~10px), ~20px gap below to row 66 |
| pr-composer | 2 | Blue `+` marker protrudes outside the row's left edge | **fixed** (marker now inside the gutter at x≈525) |
| pr-composer | 3 | Textarea focus ring inset from card right; nested blues | **remains** (textarea right edge ~17px inside the card border; card accent flush left) |
| pr-thread | 1 | Sidebar wraps paths mid-token (`competitio`/`n`) | **fixed** — wraps at `/` boundaries |
| pr-thread | 2 | `Suggested change` heading flush left; inner box inset and ends ~45% width | **remains** (heading still flush with the outer border, inner box inset ~15px and ends at ~60% width) |
| pr-thread | 3 | Suggested-change rows: no −/+ gutter, no red/green, no line numbers | **remains** |
| pr-thread | 4 | Thread header stacks three rows with ~20px gaps | **remains** (unresolved/Resolve row, author row, body still separate rows, gaps ~15–20px) |
| pr-thread | 5 | `‹ prev`/`next ›` placement depends on path length | **fixed** — both sit on the file-header row beside `Reviewed` on the long ground.html path |
| pr-thread | 6 | Line 4 nearly touches the right edge; line 21 wraps with hanging indent | **remains** (line 4 ends ~40px short of the border, line 21 wraps; hanging indent consistent) |

## New / remaining problems per image

### pr-unified.png
1. Row 66 (blank) is still shorter than neighbours; the hunk header row is taller than code rows, so there are three row heights in a 7-line hunk.
2. At 1440 the Files sidebar wraps `internal/web/templates/admin.html` etc. onto two lines even though the stat column (`+2 −0`) leaves ~120px free; the wrap point is well before the stats column.
3. `Threads 0` card: a 640px-wide card for a two-word label (equal-thirds grid).

### pr-split.png
1. Right pane line 68 wraps `"--force-dev-mode"` mid-string-literal at the hyphen (`"--force-dev-` / `mode"} {`), so a string literal is split across rows in the diff.
2. Continuation rows (line 70 both panes) begin under the `−`/`+` sign column, not under the code column; they read as outdented statements (r4 #2, now slightly worse because the sign column is wider than the hang).
3. Left row 68 shows a full-height red band (2 lines) whose second line is empty, because the right side wrapped.

### inbox.png
1. Top-right legend now reads `● open ○ draft ⊢ merged ✕ closed | checks ✔ pass ✕ fail ● running`: `✕` means both *closed* (state) and *fail* (check) in the same legend line with the same glyph and colour.
2. Grey `●` in the right status column (rows 3, 4, 6, 9, 10, 12, 19, 21, 23) is not in the legend.
3. PRs #251, #248 and #130 appear in both `Needs your review` and `Returned to you`; each shows a different reason (`re-requested after changes` vs `changes requested`), so the same PR is listed twice with conflicting states.
4. Section toggle glyphs (▾/▸) remain tiny (~7px) and are the only affordance for collapsed sections.
5. Meta line grey on card background still ~3.5:1.

### pr-composer.png
1. Composer card is inset ~20px from the diff-card edge on both sides while its anchor row (65) runs full width; the blue accent bar (x≈537) and the `+` marker (x≈525) are on different verticals.
2. `65` in the anchor row shifts ~5px left versus the other rows' numbers because the `+` marker shares the cell.
3. Textarea focus ring (inner blue) and card accent (outer blue) remain two nested blues; the header text `New comment  line 65, old side` and the hint `shift-click a line number to extend` are on the same baseline with ~800px between them.

### pr-thread.png
1. Suggested-change inner box renders in the proportional UI font, not monospace, and its two rows have no gutter, sign, colour, or line numbers; the removed and added rows look identical.
2. The inner box is left-padded ~65px so the code starts well right of the `Suggested change` heading, and the box ends at ~60% of the outer box width.
3. Thread card is inset ~18px inside the green diff rows above and below, so a green sliver shows on both sides of the card.
4. `Reply` button sits alone at the bottom-left with ~30px above it; no `Add to review`/apply-suggestion action for the suggestion.
5. Author line `@copilot-pull-request-reviewer  1y ago ↗` and the `unresolved`/`Resolve` row are ~35px apart; the pill is amber text on dark amber, the `Resolve` link is blue text: three colours in the header.

### pr-cards.png (vitriolic #123, all three summary cards opened)
1. **Horizontal overflow.** Opening `Threads` makes the page 1574 CSS px wide (3148 device px): the three thread links (`…/competition/venue.html:8`, `…/ground.html:8`, `…/test_competition_site.py:209`) do not wrap or truncate and extend ~170px past the card's right border and past the viewport. Long paths need `overflow-wrap: anywhere` / ellipsis.
2. `Checks none` opened shows an empty body: the card just grows ~20px with no content and no "no checks" message.
3. Reviews body: the Copilot review markdown list loses its structure. Each bullet renders as `Enhanced  competition_by_slug` on one line, then the rest (`decorator to extract and validate …`) as a separate paragraph; no bullet markers, so it reads as broken sentences. The `<details>` blocks (`Show a summary per file`, `Comments suppressed …`) render as full-width select-like boxes with a ▾ at the far right.
4. The three cards are `align-items: start` so `Checks` (empty) is 50px tall, `Threads` ~330px and `Reviews` ~730px, with the Conversation bar below sitting under the tallest; the grid does not reflow.
5. Thread entries show only a path link and author line; no comment excerpt, so the list gives no idea what each thread is about.

## Other captures (not viewed; sizes only)
pr-review-dialog, settings, inbox-light, pr-light, pr-split-wide captured without errors.
