# Ampcode-like TUI Theme — Visual Direction

## 톤앤매너

- **키워드**: near-black / low-contrast gray / sparse green accent / terminal-native / quiet
- **설명**: 현재 Roach TUI는 blue accent가 넓고 밝게 쓰이며, 배경이 보라/갈색이 섞인 charcoal로 보여 Ampcode와 거리가 있다. Ampcode 쪽은 거의 black canvas, 낮은 대비의 gray text, 제한적인 green-cyan accent를 중심으로 한다. 색을 많이 쓰지 않고, 텍스트 위계와 여백으로 UI를 읽게 해야 한다.

## 레퍼런스

| # | 레퍼런스 | 참고 포인트 |
|---|----------|-------------|
| 1 | 사용자 제공 스크린샷 `image-1782094754142.png` | 현재 Roach 화면. 배경이 `#302830` 근처로 따뜻하고, 로고/입력선 blue가 넓게 보여 Ampcode와 다르게 느껴짐. |
| 2 | Amp CLI welcome screenshot, Sid Bharath guide | Amp 화면은 black canvas가 압도적이고, dot-matrix 로고의 어두운 green accent와 muted gray text가 중심. |
| 3 | Amp "Look Ma, No Flicker" 공식 글 | Amp TUI의 핵심은 full-screen, responsive, smooth overlays/popups, no-flicker foundation. 색상보다 terminal-native calm surface가 중요. |

## 관찰 요약

### Amp reference sampled impression

- Background dominant: `#000000` bucket이 대부분.
- Secondary darks: `#080808`, `#101010`, `#181818`, `#202020`.
- Text: 대략 `#707070`~`#808080` muted gray, 일부 near-white label.
- Accent: blue가 아니라 very dark green/cyan 계열. sampled bucket 기준 `#102018`, `#203028`, `#283830`, `#304038` 근처.
- Logo: solid block hero가 아니라 sparse dotted glyph. 색도 낮은 채도/낮은 밝기.

### Current Roach screenshot sampled impression

- Background dominant: `#302830`, warm purple/brown charcoal.
- Accent/text: `#7088c8`, `#9098b8`, `#b8c0d0` 등 blue-gray가 넓은 면적으로 노출.
- Input border: bright blue horizontal rule이 화면 하단을 강하게 지배.
- Logo: large filled gradient block, Amp보다 훨씬 decorative하고 밝음.

## 컬러 방향

| 용도 | 현재 토큰 | 현재값 | 변경 방향 | 근거 |
|------|-----------|--------|-----------|------|
| Background canvas | `cliDarkTheme.ink` | `#0d0d0f` | `#000000` 또는 `#030303` | Amp welcome reference는 black canvas가 거의 전부. 사용자 스크린샷처럼 terminal background가 `#302830`으로 보이면 Amp와 멀어짐. |
| Surface | `surface` | `#18181b` | `#080808` | floating panel 느낌보다 black 위의 미세한 층만 허용. |
| Surface lift | `surfaceLift` | `#1c1c1f` | `#101010` | top/lift도 강하게 보이지 않아야 함. |
| Border / divider | `border`, `surfaceSeam` | `#27272a`, `#232327` | `#181818`~`#202020` | border가 UI를 만들면 Amp 느낌이 줄어듦. 구분선은 거의 배경에 묻히게. |
| Primary text | `text` | `#d4d4d8` | `#d8d8d8` only for strong labels | Amp는 대부분 muted text이며, near-white는 "Welcome to Amp" 같은 소수의 label에만 사용. |
| Muted text | `muted` | `#a1a1aa` | `#8a8a8a` | 현재보다 더 어둡고 중립적인 gray. |
| Faint text | `faint` | `#71717a` | `#5f5f5f` | footer/meta/help text는 더 낮은 대비로. |
| Primary accent | `accent` | `#6b8cce` | `#48a36d` 또는 `#3f8f62` | Amp welcome의 시각적 accent는 blue보다 green-cyan dot matrix에 가까움. |
| Selection | `selection` | `#6b8cce` | `#48a36d` but sparse | selection marker/glyph만 green. full-width border에는 사용하지 않음. |
| Tool read | `toolRead` | `#6b8cce` | `#7a7a7a` or accent only for glyph | tool rows는 color-coded보다 muted summary 중심. |
| Tool proc | `toolProc` | `#94a3b8` | `#6f6f6f` | secondary tool categories are neutral, not purple/blue. |
| Success | `success` | `#4ade80` | `#5ba870` | green은 유지하되 채도 낮춤. |
| Warning | `warn` | `#fbbf24` | `#b58a45` | amber는 permission/risk에만 제한. |
| Error | `err`, `danger` | `#f87171`, `#ef4444` | `#c85f5f`, `#d06464` | terminal-native warning tone, not bright app-red. |
| Input border | `inputBoxStyle border accent` | `#6b8cce` | `#202020` border + cursor only bright | 현재 가장 Amp와 다른 지점. 하단 blue rule을 없애야 함. |
| Logo color | shimmer/hero gradient | blue-gray gradient | dotted dark green, or remove large hero gradient | Amp welcome logo is sparse/dim. Roach filled gradient logo is too loud. |

## 추천 토큰 세트

### Dark / Amp-like target

| 토큰 경로 | 제안값 | 적용 대상 |
|-----------|--------|-----------|
| `ink` | `#000000` | alt-screen canvas / terminal background assumption |
| `surface` | `#080808` | rare elevated rows only |
| `surfaceLift` | `#101010` | subtle local lift |
| `surfaceSeam` | `#181818` | separators |
| `border` | `#1a1a1a` | input/panel rules |
| `text` | `#d7d7d7` | high-emphasis title/answer text |
| `muted` | `#8a8a8a` | normal labels, header model/cwd |
| `faint` | `#5f5f5f` | hints/meta/footer secondary text |
| `accent` | `#48a36d` | sparse prompts, active marker, logo dots |
| `selection` | `#48a36d` | active row marker only |
| `toolRead` | `#8a8a8a` | tool label text, not large color blocks |
| `toolProc` | `#6f6f6f` | process/tool secondary |
| `success` | `#5ba870` | completed status |
| `warn` | `#b58a45` | permission/risk warning |
| `err` | `#c85f5f` | errors |
| `danger` | `#d06464` | destructive approval emphasis |

### Xterm fallback target

| 의미 | 제안 xterm | 메모 |
|------|------------|------|
| black canvas | `16` | true black |
| near-black surface | `232` | very dark gray |
| subtle border | `234` | low-contrast divider |
| faint | `240` | muted gray |
| muted | `245` | medium gray |
| text | `188` or `252` | terminal에 따라 너무 밝으면 `188` |
| accent green | `65` or `72` | blue보다 green-cyan 쪽 |
| warn | `136` | subdued amber |
| err/danger | `131` / `167` | subdued red |

## 타이포그래피 방향

| 요소 | 현재 설정 | 변경 방향 | 근거 |
|------|-----------|-----------|------|
| Welcome logo | large filled ROACH block with blue-gray shimmer | dotted or much dimmer logo; ideally one accent color only | Amp welcome은 sparse dot matrix. 현재 filled gradient logo가 제품 identity를 너무 강하게 만듦. |
| Header | bold-ish model/cwd line | keep single line, reduce contrast to muted/faint | Amp는 chrome보다 content가 조용함. |
| Body | large low-contrast text in screenshot | keep monospace, but reduce decorative gradient | Terminal-native readability. |
| Footer | `Auto`, `ready`, model, effort, balance | keep two rows, reduce blue use to `Auto` marker/cursor only | 하단 blue line이 Amp와 가장 다름. |

## 간격 및 레이아웃

- spacing 기본 단위: terminal cell 2칸 indent 유지.
- 큰 bordered input rectangle은 유지하더라도 border color는 accent가 아니라 near-black border로 낮춘다.
- accent는 "면"이 아니라 "점/커서/선택 marker"에만 쓴다.
- welcome screen의 빈 공간은 유지하되, 로고의 시각적 무게를 절반 이하로 낮춘다.
- 메뉴/패널은 이미 borderless overlay 방향이 맞다. 다음은 color contrast를 낮추는 단계.

## 변경 필요 토큰 요약

| 토큰 경로 | 현재값 | 변경값 | 적용 대상 |
|-----------|--------|--------|-----------|
| `cliDarkTheme.ink` | `#0d0d0f` | `#000000` | 전체 TUI canvas |
| `cliDarkTheme.accent` | `#6b8cce` | `#48a36d` | active marker, sparse accent |
| `cliDarkTheme.selection` | `#6b8cce` | `#48a36d` | selected rows |
| `cliDarkTheme.muted` | `#a1a1aa` | `#8a8a8a` | model/cwd/body secondary |
| `cliDarkTheme.faint` | `#71717a` | `#5f5f5f` | meta/help/footer |
| `cliDarkTheme.border` | `#27272a` | `#1a1a1a` | input border, separators |
| `cliDarkTheme.surface` | `#18181b` | `#080808` | rare elevated surfaces |
| `cliDarkTheme.surfaceLift` | `#1c1c1f` | `#101010` | subtle surface lift |
| `cliDarkTheme.surfaceSeam` | `#232327` | `#181818` | separators |
| `cliDarkTheme.toolRead` | `#6b8cce` | `#8a8a8a` | read/tool label |
| `cliDarkTheme.toolProc` | `#94a3b8` | `#6f6f6f` | process/tool secondary |
| `cliDarkTheme.success` | `#4ade80` | `#5ba870` | done states |
| `cliDarkTheme.warn` | `#fbbf24` | `#b58a45` | warning states |
| `cliDarkTheme.err` | `#f87171` | `#c85f5f` | error states |
| `cliDarkTheme.danger` | `#ef4444` | `#d06464` | destructive states |
| `inputBoxStyle` border color | `activeCLITheme.accent` | `activeCLITheme.border` | input top/bottom lines |
| `scrollThumbStyle` | `accent` | `faint` or `border` | scrollbar should not read as primary brand stripe |
| welcome banner gradient | blue-gray gradient | dark green dot/very dim single-accent treatment | welcome state |

## 구현 순서 제안

1. Token-only pass: `cliDarkTheme` values and input/scroll border colors only.
2. Welcome logo pass: blue-gray filled shimmer를 제거하거나 dark green dotted/low-density treatment로 축소.
3. Snapshot/smoke pass: 80x24, 120x40 screenshots에서 sampled dominant background가 black/near-black이고, blue bucket이 top colors에서 사라지는지 확인.
4. Keep light theme separate: 이번 Amp-like matching은 dark theme 우선. light theme은 현재보다 neutral하되 Amp reference와 직접 매칭하지 않는다.

## 승인 필요 사항

- Accent를 blue에서 muted green으로 바꾸는 방향을 승인할지.
- ROACH welcome logo를 Amp처럼 sparse/dim하게 줄일지, 아니면 Roach identity 때문에 유지하되 채도만 낮출지.
- Terminal background를 실제로 `#000000`으로 paint할지, 아니면 foreground-only 원칙을 유지하고 사용자의 terminal theme에 맡길지.
