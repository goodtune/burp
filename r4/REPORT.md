# UI review r4 — burp @ cc22dec (branch ui-review, from origin/claude/github-pr-code-review-app-ycei2p)

## Environment / method
- Built `CGO_ENABLED=0 go build -o burp ./cmd/burp`, restarted `./burp serve` with repo `.env`; `/healthz` → `ok`.
- Playwright Chromium via `.driver/shot.mjs`, 1440x900 (pr-split-wide 1920x1080), deviceScaleFactor 2, dark unless stated, `load` + `networkidle` (8s cap) + 1500ms, full-page capture.
- Session: `./burp dev-session 286798` (@goodtune).
- Thread capture targets the Files entry ending in `competition/ground.html` (`THREAD_PATH` env added to shot.mjs).

## Timings
| capture | wall (incl. browser launch + networkidle cap) | action timing |
|---|---|---|
| inbox.png | 14.3s | — |
| pr-unified.png | 11.7s | — |
| pr-split.png | 13.0s | split-ready 1418ms |
| pr-review-dialog.png | 12.6s | — |
| pr-composer.png | 13.1s | click 66ms, composer-visible 1369ms |
| settings.png | 3.0s | — |
| inbox-light.png | 10.3s | — |
| pr-light.png | 11.2s | — |
| pr-split-wide.png | 13.0s | split-ready 1376ms |
| pr-thread.png | 12.9s | thread-visible 885ms |

Wall times for PR/inbox pages are dominated by the 8s networkidle cap (page keeps polling); settings has no background requests.

## Console errors / HTTP ≥400
None on any capture (`.driver/console.log` empty).

## r3 findings — status
| page | # | r3 finding (short) | r4 status | note |
|---|---|---|---|---|
| pr-unified | 1 | no bottom padding after last row | **fixed** | ~20px padding below line 71 |
| pr-unified | 2 | left-ellipsis path truncation, ragged | **fixed** | paths now wrap at directory boundary (`internal/web/templates/` / `admin.html`) |
| pr-unified | 3 | blank line 66 shorter than other rows | **remains** | still ~24px vs 29px |
| pr-unified | 4 | pencil glyph baseline / accent corner | **fixed** | glyph centred, accent rounded both ends |
| pr-unified | 5 | dashed `@goodtune · requested` chip | **remains** | still the only dashed pill |
| pr-unified | 6 | tiny low-contrast chevrons; Threads has none | **remains** | unchanged |
| pr-split | 1 | code start shifts between Unified and Split | **remains** | split code starts ~70px left of unified (gutter is narrower in split) |
| pr-split | 2 | wrapped continuation lines have no hanging indent | **improved** | continuation now hangs at the code column start; no wrap marker, and it hangs left of the line's own indentation |
| pr-split | 3 | row 68 red band taller than its content | **improved** | right side now 2 lines (was 3); left still shows a 1-line empty red band |
| pr-split | 4 | numbers right-aligned far from code | **fixed** | numbers left-aligned next to sign column |
| pr-split | 5 | row 66 short on both sides | **remains** | |
| pr-split | 6 | missing bottom padding | **fixed** | |
| pr-split-wide | 1 | centred max-width wastes 145px each side | **fixed** | full-width layout, ~20px margins |
| pr-split-wide | 2 | lone `{` wrapped on line 69 | **fixed** | no wraps at 1920 |
| pr-split-wide | 3 | sidebar fixed 330px, paths still truncated | **fixed** | sidebar ~470px, all paths single-line |
| pr-split-wide | 4 | Threads card mostly empty | **remains** | 640px card with `Threads 0` |
| pr-split-wide | 5 | no bottom padding | **fixed** | |
| inbox | 1 | mixed glyph weights in status column | **improved** | neutral is now a solid grey dot rather than a thin ring; ✔/✕ still line icons of different visual weight to the dot |
| inbox | 2 | left dot has no legend | **fixed** | `● open ○ draft ⑂ merged ✕ closed` legend top-right |
| inbox | 3 | meta line contrast; `re-requested after changes` not distinct | **remains** | |
| inbox | 4 | tiny ▾/▸ section toggles | **remains** | |
| inbox | 5 | `Nothing here.` left rail inconsistent | **improved** | now starts at the dot column (x≈62) but PR text starts at x≈85 |
| inbox | 6 | filter placeholder + Syntax hint two greys / small ↗ | **remains** | |
| pr-composer | 1 | double blue border (card + textarea) | **improved** | card border is now a blue left accent only; textarea keeps the ring |
| pr-composer | 2 | card aligned to neither gutter nor container | **fixed** | card spans gutter → card right edge |
| pr-composer | 3 | two blues for `+` marker vs button | **fixed** | same blue |
| pr-composer | 4 | muddy olive anchor highlight | **fixed** | anchor row is now a subtle blue tint |
| pr-composer | 5 | composer floats with gaps above/below | **improved** | symmetrical ~20px gaps, still detached from row 65 |
| pr-composer | 6 | shift-click hint looks like header text | **fixed** | muted caption |
| pr-thread | 1 | thread card inset to code column | **fixed** | spans gutter → right edge |
| pr-thread | 2 | body text clipped at right edge | **fixed** | wraps |
| pr-thread | 3 | suggested change has no −/+ gutter, colour, or numbers; centred | **improved** | left-aligned now, but still no −/+ gutter, fill colour, or line numbers, so it still doesn't read as a diff |
| pr-thread | 4 | suggested-change rules stop short of card edge | **improved** | outer box is full width; the inner diff box ends at ~45% of the card width |
| pr-thread | 5 | `unresolved` pill on its own row | **improved** | row now also carries `Resolve`; still three stacked header rows before content |
| pr-thread | 6 | `💬 1` misaligns add/del column | **fixed** | marker sits left of the counts, counts right-aligned |
| pr-thread | 7 | `Reply` bare link | **fixed** | button |
| pr-thread | 8 | inconsistent wrapping of long lines | **fixed** | line 21 wraps with hanging indent |

Totals: 21 fixed, 8 improved, 9 remain.

## New / remaining problems per image

### pr-unified.png
1. Blank row 66 is still ~5px shorter than its neighbours (r3 #3); the diff's vertical rhythm is uneven.
2. Files sidebar (~1140px tall) and diff card (~880px) end at very different heights; the sidebar's wrapped two-line paths push its footer legend (`changed since reviewed / unresolved / drafts` + `shortcuts`) onto two rows, while at 1920 it fits one row — the legend layout depends on incidental sidebar width.
3. `@goodtune · requested` remains the only dashed pill on the page.

### pr-split.png
1. Toggling Unified → Split still moves the code start (x≈715 → ≈645 css-ish) because the split gutter is narrower; text jumps on toggle.
2. Wrapped continuation of line 70 (`output:\n%s", want, output)`) hangs at the code column's left edge, which is *left* of the line's own 4-level indent, so the continuation reads as an outdented new statement.
3. Row 68 left side is padded to two lines with a full-width empty red band.
4. Row 66 short (as above).
5. In the right column the `+` sign sits ~15px from the number but ~55px from the code; the left column's `−` has the same asymmetry. Sign column is wide relative to the tight number column.

### pr-split-wide.png (1920x1080)
1. Right column line 70 ends at `output)` ~10px from the card border — no right padding on the right pane, so long lines touch the edge before wrapping.
2. Threads card: 640px wide with only `Threads 0`; the three-card row still allocates equal widths regardless of content.
3. Row 66 short.

### inbox.png
1. The right-hand status column (✔ / ✕ / ●) has no legend and no header; the new top-right legend only covers the left-hand state dot. ✔/✕ are line icons, ● is a filled dot — three glyphs, two styles.
2. `Nothing here.` still does not align with PR titles (starts at the dot column).
3. Meta line grey vs card background is still low contrast (~3.5:1); `re-requested after changes` is not visually distinct from `review requested`.
4. Section toggles remain ~7px glyphs.
5. Filter placeholder and `Syntax: GitHub search qualifiers ↗` still two greys, ↗ smaller than its text.

### pr-composer.png
1. Composer card is separated from its anchor row 65 by ~20px above and below; the row-to-card connection is only the shared blue tint.
2. The blue `+` square marker at the gutter edge protrudes ~4px outside the row's left boundary (x≈523 vs row start ≈518).
3. Textarea focus ring is inset ~18px from the card's right border while the card's left blue accent is flush; the two blues are still nested rather than one.

### pr-thread.png (vitriolic #123, ground.html)
1. Files sidebar now wraps paths mid-token: `tournamentcontrol/competitio` / `n/templates/tournamentcontro` / `l/competition/ground.html` — breaks inside `competition` and `tournamentcontrol`. The ghp page wraps at `/` boundaries, so wrapping is word-break: break-all here (probably because the path segment exceeds the sidebar width).
2. `Suggested change` heading is flush against the box's left border (no padding) while the inner diff box below it is inset ~15px; the inner box ends at ~45% of the card width, leaving the right half of the outer box empty.
3. Suggested change rows have no −/+ gutter, no red/green fill, and no line numbers; the removed and added lines look identical apart from content.
4. Thread header stacks three rows (`unresolved … Resolve`, author line, body) with ~20px between each.
5. File header: `‹ prev` / `next ›` sit on a second row beneath the long file path, but on ghp #54 `next ›` sits on the header row right next to `Reviewed` — placement changes with path length.
6. Line 4 (`{% block page_title %}…`) reaches x≈1770 of 1850, while line 21 wraps; hanging-indent continuation `</td></tr>` aligns with the code start, consistent with split view.

## Other captures (not viewed; sizes only)
- pr-review-dialog.png 556KB, settings.png 160KB, inbox-light.png 971KB, pr-light.png 377KB — captured without console errors.
