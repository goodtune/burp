# UI review r3 — burp @ fa36918 (branch ui-review)

## Environment / method
- Rebuilt fa36918 (`CGO_ENABLED=0 go build -o burp ./cmd/burp`), no prior `burp serve` was running, started `./burp serve` with .env exported (macOS has no `setsid`; used `nohup … &`). `/healthz` → `ok`. Left running on :8080 (pid 12821), log in .driver/burp.log.
- Capture: Chromium 1440x900 @2x (1920x1080 for pr-split-wide), load + best-effort networkidle (8s cap) + 1500ms. Cookie from `burp dev-session 286798` (goodtune).
- shot.mjs gained `VW`/`VH` env overrides and a `thread` action (click the first `li.file` in the Files list with an `.unresolved` marker, wait for `#pr-diff .thread`).
- Timings: Split → `table.diff.split` present after **1419ms** (1440) / **1384ms** (1920). Composer: click **65ms**, `.composer` visible **1376ms** after click. Thread: file click → `.thread` visible **1381ms**.
- Console log empty: no console errors, page errors, or 4xx/5xx across all ten captures.
- pr-thread.png is goodtune/vitriolic #123 (file `…/competition/ground.html`, 1 unresolved thread from @copilot-pull-request-reviewer).

## r2 findings — status
| r2 finding | r3 |
|---|---|
| Double blue border on composer card + textarea | **Remains** — outer card and textarea both have a blue 2px border |
| Composer card inset ~130px left / ~15px right | **Remains** — card starts at the code column (x≈625) not the gutter, right edge inset ~20px |
| Blue `+` gutter marker overlaps cell border, different blue from buttons | **Improved** — marker now sits inside the gutter cell; still a lighter blue than `Add to review` |
| Amber anchor-row highlight leaves a dark notch under second line-number column | **Fixed** — highlight now spans the full row |
| `Add to review` taller than `Cancel` | **Fixed** — both buttons the same height |
| Split: right column overflows / cut at card edge | **Fixed** — long lines now wrap in both columns |
| Split: left/right cells styled asymmetrically on row 68 | **Fixed** — red and green fills each span their half incl. gutter |
| Split: no vertical divider between columns | **Fixed** — 1px divider present |
| Split: code indented differently from unified | **Remains** — code starts ~8px further right in split vs unified |
| Checks card only one with a body; Reviews/Threads collapse | **Fixed** — all three cards are one-row headers of equal height |
| Files sidebar left-ellipsis truncation inconsistent | **Remains** (`…ternal/web/templates/admin.html` vs `…al/web/templates/dashboard.html`) |
| Diff card ends flush after line 71 | **Remains** — line 71 still touches the card border (all diff views) |
| Hunk header styled like a code row, no divider | **Improved** — hunk header now has a distinct dark-blue band |
| `next ›` anchoring vs `Reviewed` | **Improved** — `Reviewed` and `next ›` grouped at right |
| Inbox neutral ○ smaller/thinner than ✔/✖ | **Remains** |
| Inbox left coloured dot unexplained | **Remains** (green dot on #258, #2, #427; hollow ○ elsewhere) |
| Inbox mixed relative/absolute dates | **Fixed** — all relative (`11d ago`, `7mo ago`, `9y ago`) |
| "search qualifiers ↗" floats without separator | **Improved** — now labelled `Syntax: GitHub search qualifiers ↗`, still no visual grouping with the input |
| Section "▾" toggle near-invisible | **Remains** |
| Meta line low contrast | **Remains** — `re-requested after changes`, `review requested` are ~#8a8f98 on #16191e |

## pr-unified.png
1. Diff card has no bottom padding: line 71's row touches the rounded card border, while the Files sidebar has ~24px below `shortcuts`; the two columns end at different heights (1075 vs 870 css px).
2. Files list truncation is left-ellipsis with inconsistent cut points (`…ternal/web/templates/admin.html`, `…al/web/templates/dashboard.html`, `…ternal/web/templates/login.html`); the `internal/` prefix is lost and the three rows look ragged.
3. Row 66 (blank line) is rendered at ~2/3 the height of the other rows (≈20px vs 29px), so the code block has an uneven vertical rhythm.
4. Draft banner: the pencil glyph is vertically ~2px above the text baseline of `Draft`, and the banner's left amber accent is rounded at the top but square at the bottom.
5. Reviews card: `@goodtune · requested` chip has a dashed border unlike every other pill (solid `3`, `1 failing`, `0`); the dashed style isn't explained.
6. Chevron `▸` on the Checks / Reviews / Conversation cards is ~8px and low contrast, hard to see as an affordance; Threads card has no chevron at all despite being the same card type.

## pr-split.png
1. Left column's code starts ~8px further right than in unified mode (x≈870 vs 660 relative to the gutter), so toggling Unified/Split shifts text; the `−`/`+` sign column is also narrower than in unified.
2. Wrapped continuation lines (68, 69, 70) have no hanging indent and no visual wrap marker; `[]string{"--migrate"} {` reads as a separate statement under `for _, want := range`.
3. Row 68: the wrapped right side is 3 lines tall while the left is 2, and the empty extra line on the left is filled red — a full-width red band with no content.
4. Line-number gutters are right-aligned against the divider with ~30px of dead space to their left in each column; the numbers sit far from the code they label on the left half (`65` at x≈845, code at x≈910).
5. Row 66 (empty line) is again a shorter row than its neighbours, now on both sides.
6. Same missing bottom padding after line 71.

## pr-split-wide.png (1920x1080)
1. The page is centred with a max-width (~1710px) so at 1920 there is ~145px of unused margin each side, yet the split columns still wrap line 68/69/70; the wide viewport buys nothing for the diff.
2. Row 69's right side wraps a lone `{` onto its own line (`if !strings.Contains(output, want)` / `{`); a hanging indent or slightly narrower gutter would avoid single-token wraps.
3. Files sidebar is fixed at ~330px, so with the wider diff area the sidebar/diff ratio is 1:4 and the sidebar looks narrow; the truncated paths remain truncated even though there is room.
4. Top summary cards: Checks and Reviews are ~560px each but Threads is ~560px with only `Threads 0` in it — the third card is mostly empty at this width.
5. Bottom of the diff card still touches line 71 (no padding).

## inbox.png
1. Status column at far right mixes three glyph weights: ✔ and ✖ are 1.5px line icons, the neutral ○ is a thinner 1px ring ~2px smaller; at a glance neutral rows look like they have no status.
2. The left-hand status dot (● green on 3 rows, ○ hollow elsewhere) has no tooltip/legend; readers can't tell green = unread/new vs hollow.
3. Meta line contrast: `copilot-swe-agent · 11d ago · … · review requested` is ~#8a8f98 on #16191e (≈3.4:1) and the `re-requested after changes` variant is not distinguishable in weight or colour from `review requested`.
4. The `▾`/`▸` section toggles at the far left of each section header are ~7px and very low contrast; the header row's only clear affordance is the text.
5. `Nothing here.` under "Approved, ready to merge" is left-aligned to the card edge (x≈38) while the PR rows in other sections start at x≈62 (after the dot) — inconsistent left rail.
6. Filter input placeholder `Filter, e.g. repo:acme/api label:bug` and the `Syntax: GitHub search qualifiers ↗` hint are two separate elements with different greys and a 16px gap; the ↗ glyph is smaller than its text.

## pr-composer.png
1. Double blue border remains: the composer card (2px blue) and the textarea (2px blue focus ring) nest ~20px apart; the textarea is the only thing that should carry the ring.
2. Card alignment: left edge starts at the code column (x≈625), not the line-number gutter (x≈500), while the right edge is inset ~20px from the card edge — it aligns to neither the code nor the container.
3. The blue `+` marker in the gutter is a lighter/saturated blue (#3b82f6-ish) than the `Add to review` button (#2f6fdb-ish); two blues for the same "active" meaning.
4. The anchor row's amber highlight is a muddy olive (#4a3f10-ish) on the dark theme; against the green/red diff rows below it reads as a third diff state rather than a selection.
5. The composer `tr` has a ~28px gap above the card and ~28px below, but the rows on either side (65 and 66) have no gap; the card floats rather than being anchored to line 65.
6. `shift-click a line number to extend` hint is right-aligned at the same size as the header's `line 65, old side` and reads as part of the header; it could be a muted caption.

## pr-thread.png (vitriolic #123)
1. Thread card is inset to the code column (x≈605) like the composer, leaving a ~125px dead band over the gutter; the card's right edge runs to the diff edge with no matching inset.
2. Comment body text is cut off at the right edge: `…or use a {% blocktrans %} if` is clipped by the card boundary with no wrap or scroll.
3. `Suggested change` block: the diff table has ~260px of empty left padding before the removed/added lines and the lines are centred rather than left-aligned; there is no `−`/`+` gutter, colour fill, or line number, so it doesn't read as a diff.
4. The suggested-change table's top/bottom rules stop ~470px short of the card's right edge, so the block looks like an unfinished table.
5. `unresolved` pill sits alone on its own row above the author line with ~20px of padding; combined with the author row and `1y ago ↗` it's three stacked header lines before content.
6. Files sidebar: rows with a thread marker (`💬 1`) push `+26 −0` right so the add/del column no longer aligns with rows that have no marker; path truncation gets more aggressive (`…ol/competition/ground.html`).
7. `Reply` is a bare blue link with no button affordance and no separator from the body; contrast with `Mark ready for review` and `Review…` which are full buttons.
8. Line 4 of the diff has a very long line that is wrapped in unified mode here but line 21 is not (`…{{ match.get_away_team.title }}</td><…` is clipped at the card edge), so wrapping is inconsistent within the same file.

## Other captures
pr-review-dialog.png, settings.png, inbox-light.png, pr-light.png captured, not reviewed in detail.
