# Third-Party Notices

Roach Code is distributed as a single static Go binary that statically links the
open-source modules listed below. All are under permissive licenses (MIT or
BSD-3-Clause). Each module's full license text ships with its source and is
available in the Go module cache (`go env GOMODCACHE`) and at its upstream
repository.

Roach Code itself is a multi-model rewrite of
[deepseek-reasonix](https://github.com/esengine/deepseek-reasonix); see
[`LICENSE`](./LICENSE) for its (MIT) terms and the preserved upstream notice.

Regenerate this file with full embedded license texts via:

```sh
go run github.com/google/go-licenses@latest report ./cmd/roach-code
```

## Direct dependencies

| Module | License |
|---|---|
| charm.land/bubbles/v2 | MIT |
| charm.land/bubbletea/v2 | MIT |
| charm.land/lipgloss/v2 | MIT |
| github.com/BurntSushi/toml | MIT |
| github.com/alecthomas/chroma/v2 | MIT |
| github.com/atotto/clipboard | BSD-3-Clause |
| github.com/charmbracelet/x/ansi | MIT |
| github.com/mattn/go-runewidth | MIT |
| github.com/sabhiram/go-gitignore | MIT |
| github.com/yuin/goldmark | MIT |
| go.uber.org/goleak | MIT |
| golang.org/x/net | BSD-3-Clause |
| golang.org/x/sys | BSD-3-Clause |
| golang.org/x/term | BSD-3-Clause |
| golang.org/x/text | BSD-3-Clause |

## Indirect dependencies

| Module | License |
|---|---|
| github.com/charmbracelet/colorprofile | MIT |
| github.com/charmbracelet/ultraviolet | MIT |
| github.com/charmbracelet/x/term | MIT |
| github.com/charmbracelet/x/termios | MIT |
| github.com/charmbracelet/x/windows | MIT |
| github.com/clipperhouse/displaywidth | MIT |
| github.com/clipperhouse/uax29/v2 | MIT |
| github.com/dlclark/regexp2 | MIT |
| github.com/lucasb-eyer/go-colorful | MIT |
| github.com/muesli/cancelreader | MIT |
| github.com/rivo/uniseg | MIT |
| github.com/xo/terminfo | MIT |
| golang.org/x/sync | BSD-3-Clause |

> Note: license identifiers above are recorded from each module's published
> license. If you redistribute the binary, this file plus [`LICENSE`](./LICENSE)
> satisfy the notice requirements of MIT and BSD-3-Clause. For a build that
> embeds every full license text, run the `go-licenses` command above.
