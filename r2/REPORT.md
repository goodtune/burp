# UI review r2 — burp @ fede413 (branch ui-review)

## Environment / method
- Rebuilt fede413, killed the r1 `./burp serve` (pid 72276), restarted via `make run` with .env exported; `/healthz` → ok, log shows version=fede413. Left running on :8080.
- Same capture script as r1 (Chromium 1440x900 @2x, load + best-effort networkidle + 1500ms).
- Timings: Split → `table.diff.split` present after **1386ms**. Composer: normal Playwright click on the first text line-number cell succeeded in **48ms** (r1 timed out at 30s; the "not stable" problem is gone), `#pr-diff .composer` visible **1352ms** after the click.
- Console log empty: no console errors, page errors, or 4xx/5xx across all eight captures.

## r1 findings — status
| r1 finding | r2 |
|---|---|
| Split toggle highlighted but diff stayed unified | **Fixed** — real two-column table renders (~1.4s) |
| First diff rows (hunk header, line 65) hidden under sticky file header, empty band above header | **Fixed** — hunk header `@@ -65,7 +65,7 @@` and line 65 visible, no empty band |
| Composer anchor line invisible; yellow sel bar a sliver | **Fixed** — line 65 row highlighted amber with a blue `+` gutter marker |
| Composer card unevenly inset, doubled blue borders | **Partly fixed** — inset is now consistent (aligned with code column) but the outer card and the textarea still both have blue borders |
| Line-number cells never stable for a real click | **Fixed** — click completes in 48ms |
| Threads panel vertically centred / header baselines misaligned | **Fixed** — Checks / Reviews / Threads headers align in one row |
| Reviews `@goodtune` chip unexplained | **Fixed** — now reads `@goodtune · requested` |
| Legend glyphs mixed emoji/text styles | **Fixed** — consistent line icons (!, bubble, pencil) |
| Progress bar under "Files 0/11" an empty grey track | **Fixed** — removed |
| Inbox status icons inconsistent (filled ✔/✖ vs outlined ○) | **Partly fixed** — ✔/✖ now thin line icons, but the neutral ○ is still visibly smaller/thinner than them |
| Inbox left coloured dot unexplained | **Remains** |
| Inbox mono repo slug vs sans title baseline mismatch | **Improved** — slug is now sans (`goodtune/ghp #258`), baselines align |
| Filter input narrow with long placeholder; "GitHub search qualifiers" floating | **Improved** — wider input, shorter placeholder, "search qualifiers ↗" is now an explicit link but still floats with no separator |
| Mixed relative/absolute dates | **Remains** (`11d ago` vs `22 Dec 2025`) |
| Composer header mixing instruction and metadata; "(right)" jargon | **Fixed** — "New comment · line 65, old side" with the shift-click hint moved to the right |
| Segmented control selected state relying on text colour | **Fixed** — selected segment now blue text on darker fill |
| "Nothing here 🎉" oddly padded | **Fixed** — plain "Nothing here." with tight padding |
| No bottom padding after last diff row | **Remains** — line 71 still touches the card border |

## pr-unified.png — new / remaining
1. Checks card is the only one of the three top cards with a body; Reviews and Threads collapse to a single header row, so the row's right two-thirds is empty space under them — the Checks card looks like it belongs to a different grid.
2. Files sidebar rows have left-ellipsis truncation with inconsistent cut points (`…ternal/web/templates/admin.html` vs `…al/web/templates/dashboard.html`); mid-path ellipsis would keep the `internal/` prefix.
3. Diff card ends flush after line 71 (no bottom padding); the sidebar has generous padding below "shortcuts", so the two columns end unevenly.
4. Hunk header row is styled like a code row (same height, muted text) with no divider below it; it merges with line 65.
5. `next ›` button in the file header is right-aligned with ~16px inset while `+1 −1 ☐ Reviewed` is centred — three different anchors in one bar.

## pr-split.png — new / remaining
1. Right column overflows: the added line 68 and line 70 are cut at the card edge (`[]string{"--migrate",` / `expected %q`) with no horizontal scroll indicator or wrap.
2. Changed row 68: left cell has a red background across the whole left half but the right cell's green fill stops at the line-number gutter; the two halves of the same change are styled asymmetrically.
3. No vertical divider between the left and right code columns; the boundary is only implied by the second line-number gutter.
4. Left column code is indented 8px less than in unified mode (starts at x≈600 vs 660), so switching modes shifts the text.

## inbox.png — new / remaining
1. Neutral status icon (○) still smaller and thinner than the ✔/✖ line icons.
2. Left coloured dot (green/grey) still has no legend or tooltip.
3. Mixed relative/absolute dates remain.
4. "search qualifiers ↗" link sits with no separator from the input; the ↗ glyph is smaller than the text.
5. Section header rows: the tiny "▾" toggle at far left is still near-invisible against the card background.
6. Row spacing is uniform but the second line (meta) is very low contrast (#8b8b8b-ish on #101317); `re-requested after changes` is barely readable.

## pr-composer.png — new / remaining
1. Double blue border: composer card and textarea both blue; the textarea's focus ring should be the only one.
2. The composer card is inset from the code column by ~130px on the left and ~15px on the right — it is not aligned to either the gutter or the card edge.
3. The blue `+` marker in the gutter overlaps the left border of the line-number cell and is a different blue from the buttons.
4. Amber row highlight for the anchor line extends under the gutter marker but not under the second line-number column, leaving a dark notch.
5. `Add to review` primary button has ~4px more vertical padding than `Cancel`; their heights differ.

## Other captures
pr-review-dialog.png, settings.png, inbox-light.png, pr-light.png captured, not reviewed in detail.
