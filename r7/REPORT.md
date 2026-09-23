# UI review r7

Branch `claude/github-pr-code-review-app-ycei2p`, head `9b3948a`. Server: `./burp serve` dev mode, SQLite, user @goodtune (uid 286798). Captures at 1440x900 @2x (pr-split-wide 1920x1080), fresh non-persistent context per shot.

## Step 3 behaviour checks (/pr/goodtune/ghp/54)

### a. File-switch latency (click → `#pr-diff .diff-head b` shows the file)

| # | file | ms |
|---|---|---|
| 1 | cmd/ghp/serve.go | 122 |
| 2 | internal/auth/auth.go | 47 |
| 3 | internal/auth/auth_test.go | 44 |
| 4 | internal/server/server.go | 60 |
| 5 | internal/server/server_test.go | 57 |
| 6 | internal/web/handler.go | 47 |

### b. Reviewed sync (burp → GitHub)

File: `cmd/ghp/main_test.go` (first in list). Note: on the first run it was already ticked from an earlier round, so it was unticked and the check re-run from a clean state; results below are from the clean run.

| step | burp checkbox | GitHub `viewerViewedState` |
|---|---|---|
| before | unchecked | UNVIEWED |
| tick, wait 3s | checked | UNVIEWED |
| untick, wait 3s | unchecked | UNVIEWED |

**Sync is not reaching GitHub.** burp.log on each toggle:

```
level=WARN msg="github viewed state" path=cmd/ghp/main_test.go viewed=true error="graphql: Resource not accessible by integration"
level=WARN msg="github viewed state" path=cmd/ghp/main_test.go viewed=false error="graphql: Resource not accessible by integration"
```

The `markFileAsViewed` / `unmarkFileAsViewed` mutation is being sent with an integration (app installation) token, which GitHub rejects for viewer-scoped viewed state. The local burp state still toggles and the UI shows the change; the failure is logged at WARN only, no UI feedback. File left unticked.

## Timings

- split-ready: 102 ms (1440), 112 ms (1920)
- composer: click 43 ms, composer-visible 81 ms
- thread-visible (ground.html): 106 ms

## Console / HTTP errors

None. `.driver/console.log` is empty across all 11 captures; behaviour script recorded no console.error, pageerror, or HTTP ≥400.

## r6 findings

| image | # | finding | status |
|---|---|---|---|
| pr-unified | 1 | hunk header row 31px vs 21px code rows | improved — header now ~same height as code rows, still a slightly darker band; acceptable |
| pr-unified | 2 | card chevrons tiny grey `▸` | fixed — large white `>` / `⌄` chevrons on all four cards |
| pr-unified | 3 | files sidebar wraps `internal/web/templates/*.html` well before stats column | remains — three template files still wrap to 2 lines with ~90px free before the stats |
| pr-split | 1 | right pane breaks inside `dev-mode` | improved — now breaks at the hyphen (`--force-dev-` / `mode"} {`); still a wrap rather than horizontal scroll |
| pr-split | 2 | continuation lines hang under sign column; left row 68 tall red band | improved — continuation lines (68 right, 70 both) now indent to the code start; left row 68 still a 2-line red band with an empty second line |
| inbox | 1 | section chevrons small dim grey | fixed — large white chevrons |
| inbox | 2 | `⊗ closed` and `✕ fail` both red | remains |
| inbox | 3 | meta line grey contrast low | remains |
| inbox | 4 | no duplicate PRs | still fine |
| pr-composer | 1 | composer card ~10px inset from diff-card edge | remains |
| pr-composer | 2 | `65` shifted left because `+` shares the cell | fixed — `+` is now its own blue square left of the number; number aligned |
| pr-composer | 3 | focus ring and card accent two nested blues | remains (left accent bar + textarea ring) |
| pr-thread | 1 | suggestion block in UI font, no row backgrounds | fixed — monospace, red/green row backgrounds, coloured markers |
| pr-thread | 2 | inner suggestion box ends at ~48% width | fixed — spans full card width |
| pr-thread | 3 | thread card inset ~10px inside green rows | remains |
| pr-thread | 4 | no apply-suggestion action, only Reply | remains |
| pr-thread | 5 | `unresolved` pill ~35px above author line | fixed — pill inline with author/time on one line |
| pr-cards | 1 | card chevrons small; inner `<details>` look like selects | improved — card chevrons now large; the two inner `<details>` in Reviews still render as bordered boxes with a far-right `⌄` plus a left `>`, i.e. two chevrons per row |
| pr-cards | 2 | cards do not equalise; dead space under Checks/Threads | remains — Checks ~95px, Reviews ~640px, Threads ~395px; Conversation bar sits under Reviews |
| pr-cards | 3 | excerpt truncation orphans `1y ago` | fixed — author · time now precede the excerpt; ellipsis ends the line |
| pr-cards | 4 | Reviews bullets hollow `○` | fixed — solid discs |

## New / remaining problems per image

### pr-unified.png
1. `cmd/ghp/serve.go` shows ticked from a previous round (local state), and the sidebar reads `1/11 reviewed` while the inbox row for #54 also shows `1/11 reviewed`; consistent, but see step 3b — this state never reached GitHub.
2. Sidebar path wrap (r6 #3) unchanged.

### pr-split.png
1. Left-hand row 68 is still a 2-line red band because its right-hand partner wraps; the empty second line reads as a phantom deleted line.
2. Wrapped continuation of line 70 on both sides starts at the code indent rather than a visible hanging marker; hard to tell wrapped text from a new line without reading numbers.

### inbox.png
1. `⊗ closed` / `✕ fail` colour clash (r6 #2) unchanged; meta contrast (r6 #3) unchanged.
2. The `re-requested after changes` pill and the `1/11 reviewed` progress meter sit in the same meta row at different heights; the pill is ~4px taller than the text line.

### pr-composer.png
1. Composer inset (r6 #1) unchanged: ~10px gap left and right relative to the anchor row and diff rows.
2. Anchor row now has the blue `+` square on line 65 while the row's background highlight extends full width; the `+` square overlaps the left card border by ~2px.

### pr-thread.png
1. Thread card inset (r6 #3) and missing apply-suggestion action (r6 #4) unchanged.
2. The sidebar entry for `ground.html` wraps to four lines and the 1-comment badge sits on the first line, far from the filename on the fourth.
3. The diff-head path is ~860px long with no truncation; at 1440 it fits, but leaves the `+26 −0 · Reviewed · prev/next` cluster jammed against the right edge.

### pr-cards.png
1. Inner Reviews `<details>` rows show both a left `>` and a right `⌄` chevron.
2. Card height mismatch (r6 #2) unchanged.
3. Threads card: file paths wrap mid-path (`templates/` / `tournamentcontrol/...`) and each thread's path is blue link text over two lines followed by author/excerpt; scanning is slower than a single-line path with a right-aligned line number.
4. Checks card `No checks reported for this commit.` is fine, but the empty space beneath it (~540px) is the largest dead area on the page.
