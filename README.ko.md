<p align="center">
  <img src="docs/logo.svg" alt="Roach Code" width="640"/>
</p>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <a href="./README.zh-CN.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>한국어</strong>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">Spec</a>
</p>

<p align="center">
  <a href="https://github.com/tmdgusya/roach-code/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/tmdgusya/roach-code/ci.yml?style=flat-square&label=ci&labelColor=161b22&logo=githubactions&logoColor=white" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/tmdgusya/roach-code?style=flat-square&color=8b949e&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/tmdgusya/roach-code/stargazers"><img src="https://img.shields.io/github/stars/tmdgusya/roach-code.svg?style=flat-square&color=dbab09&labelColor=161b22&logo=github&logoColor=white" alt="GitHub stars"/></a>
  <a href="https://github.com/tmdgusya/roach-code/graphs/contributors"><img src="https://img.shields.io/github/contributors/tmdgusya/roach-code.svg?style=flat-square&color=bc8cff&labelColor=161b22&logo=github&logoColor=white" alt="contributors"/></a>
  <a href="https://github.com/tmdgusya/roach-code/discussions"><img src="https://img.shields.io/github/discussions/tmdgusya/roach-code.svg?style=flat-square&color=58a6ff&labelColor=161b22&logo=github&logoColor=white" alt="Discussions"/></a>
</p>

<br/>

<h3 align="center">터미널을 위한 AI 코딩 에이전트.</h3>
<p align="center">설정과 플러그인으로 구동되는 하니스 — 단일 정적 Go 바이너리이며, 프리픽스 캐싱에 맞춰 튜닝되어 긴 세션에서도 토큰 비용을 낮게 유지합니다.</p>

<br/>

> **Roach Code는 [deepseek-reasonix](https://github.com/esengine/deepseek-reasonix)([@esengine](https://github.com/esengine) 제작)의 멀티 모델 rewrite입니다.** Reasonix 하니스를 그대로 유지하면서 DeepSeek 전용에서 *모든* provider로 일반화했습니다 — Codex/OpenAI(Responses API + ChatGPT OAuth), MiniMax, GLM(Z.ai), Anthropic, 그리고 모든 OpenAI 호환 엔드포인트. 맨바닥에서 새로 만든 프로젝트가 아니라, 상류 작업의 리브랜드 + 멀티 provider 확장입니다.

<br/>

## 특징

- **설정 기반.** provider, 에이전트, 활성화된 도구, 플러그인을 모두
  `roach-code.toml`에 선언합니다. 하드코딩된 모델이 없습니다.
- **멀티 모델 & 조합 가능.** OpenAI 호환 엔드포인트라면 새 코드가 아니라 설정 한
  항목이면 됩니다. 선택적으로 두 모델(실행자 + 플래너)을 캐시가 안정적인 별도
  세션에서 함께 돌릴 수 있습니다.
- **플러그인 기반.** 외부 도구는 stdio JSON-RPC(MCP 호환)로 서브프로세스로
  실행됩니다. 내장 도구는 컴파일 시점에 자기 등록됩니다.
- **마찰 없는 배포.** `CGO_ENABLED=0` 단일 바이너리이며, 명령 하나로 6개 타깃에
  크로스컴파일됩니다. 유일한 의존성은 TOML 파서입니다.

## 설치

사전 빌드된 바이너리 (Go 툴체인 불필요) — 최신 GitHub release에서 설치합니다:

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.ps1 | iex
```

설치 스크립트가 OS/아키텍처를 자동으로 감지하고, release의 `SHA256SUMS`로 다운로드를
검증한 뒤 `roach-code`를 PATH에 놓습니다. `ROACH_VERSION`, `ROACH_REPO`,
`ROACH_INSTALL_DIR`로 재정의할 수 있습니다. 또는
[Releases](https://github.com/tmdgusya/roach-code/releases) 페이지에서 아카이브를
직접 받아도 됩니다.

### 소스에서 빌드

```sh
make build      # -> bin/roach-code
make cross      # -> dist/ (darwin|linux|windows × amd64|arm64)
```

## 빠른 시작

```sh
roach-code setup                      # 설정 마법사 → ./roach-code.toml
export DEEPSEEK_API_KEY=sk-...  # 또는 .env에 넣기 (.env.example 참고)
roach-code chat                       # 이후 /init 으로 AGENTS.md(프로젝트 메모리) 생성
roach-code run "implement the TODOs in main.go"
roach-code run --model mimo-pro "add unit tests for this function"
echo "explain this code" | roach-code run
```

## 설정

해석 순서: **flag > `./roach-code.toml` > `~/.config/roach-code/config.toml` >
내장 기본값**. 비밀 키는 `api_key_env`를 통해 환경에서 가져오며, 설정 파일에는
절대 저장되지 않습니다.

```toml
default_model = "deepseek-flash"   # 실행자; 플래너를 추가하려면 [agent].planner_model 설정
# language    = "zh"               # ui 언어; 비우면 $LANG / $ROACH_LANG 에서 자동 감지

[agent]
# planner_model = "mimo-pro"          # 선택: 저빈도 플래너
# subagent_model = "deepseek-pro"     # 선택: runAs=subagent 스킬의 기본 모델
# subagent_models = { review = "deepseek-pro", security_review = "deepseek-pro" }
auto_plan = "ask"                  # off|ask|on; 복잡한 chat 작업은 plan 모드로 시작
# auto_plan_classifier = "deepseek-flash"   # 선택; 애매한 작업에서만 호출

[[providers]]
name        = "deepseek-flash"
kind        = "openai"
base_url    = "https://api.deepseek.com"
model       = "deepseek-chat"
api_key_env = "DEEPSEEK_API_KEY"

[tools]
enabled = []   # 생략/비움 = 모든 내장 도구

[permissions]
mode  = "ask"                                # 매칭 규칙이 없을 때 writer 폴백: ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"] # 모든 모드에서 강제 차단
allow = ["bash(go test*)"]                   # 절대 묻지 않음

[sandbox]
# workspace_root = ""          # file-writer는 여기로 제한; 비우면 현재 디렉터리
# allow_write    = ["/tmp"]    # write_file/edit_file/multi_edit가 추가로 쓸 수 있는 디렉터리

[[plugins]]
name    = "example"
command = "roach-code-plugin-example"
```

권한은 각 도구 호출을 게이트합니다: `deny` > `ask` > `allow` > 폴백 (reader는 항상
허용; writer는 `mode`로 폴백). `roach-code chat`은 writer 실행 전에 묻고
(`y` 한 번 · `a` 이번 세션 · `n` 거부), `roach-code run`은 자율적으로 동작하지만
여전히 `deny`는 지킵니다. 전체 스키마와 계약은 [`docs/SPEC.md`](docs/SPEC.md)를
참고하세요.

권한은 *정책*(어떤 호출을 허용/확인할지)입니다. **샌드박스**는 *집행*입니다:
file-writer(`write_file` / `edit_file` / `multi_edit`)는 `[sandbox] workspace_root`
바깥 경로(기본값: 현재 디렉터리이므로 편집이 프로젝트 안에 머무름)를 거부하며,
심볼릭 링크와 `..`를 해석해 링크로 빠져나가지 못하게 합니다. 읽기는 제한이 없습니다.
`bash` 자체도 macOS에서는 기본으로 격리됩니다(`[sandbox] bash`, Seatbelt): 명령은
동일한 루트(및 임시/툴체인 캐시)에만 쓸 수 있고, `[sandbox] network`가 설정된
경우에만 네트워크에 접근합니다. 다른 플랫폼에서는 현재 비격리로 폴백합니다(탈출
프롬프트와 곧 추가될 Linux 지원은 `docs/SPEC.md` §9 참고).

### 플러그인 (MCP)

Roach Code는 MCP 클라이언트입니다. `[[plugins]]` 항목의 `type`이 전송 방식을
선택합니다: `stdio`(기본)는 로컬 서브프로세스를 실행하고(`command`/`args`/`env`),
`http`(Streamable HTTP)는 선택적 정적 `headers`와 함께 원격 `url`에 연결합니다
(`${VAR}` / `${VAR:-default}`는 환경에서 확장되므로 토큰이 파일에 남지 않습니다).
도구는 모델에게 `mcp__<server>__<tool>`로 노출되며, MCP의 `readOnlyHint: true`를
선언한 도구는 병렬 디스패치와 권한 reader 기본값에 합류합니다.

서버의 **prompts**는 `/mcp__<server>__<prompt>` 슬래시 명령으로 노출되고(명령 뒤에
위치 인자), **resources**는 메시지에 `@<server>:<uri>`를 써서 끌어옵니다. `/mcp`는
연결된 서버와 각 서버가 노출하는 것을 나열합니다. `make build`는
`bin/roach-code-plugin-example`도 만듭니다 — 그대로 복사해 쓸 수 있는 실행 가능한
stdio 레퍼런스 서버(`echo`, `wordcount`, `review` prompt, style-guide 리소스)입니다.

```toml
[[plugins]]                       # 로컬 stdio 서버
name    = "example"
command = "roach-code-plugin-example"

[[plugins]]                       # Streamable HTTP 기반 원격 서버
name    = "stripe"
type    = "http"
url     = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_KEY}" }
```

**이미 `.mcp.json`이 있나요?** 프로젝트 루트에 두면 Roach Code가 그대로 읽습니다 —
`mcpServers` 스펙(`command`/`args`/`env`, `type`/`url`/`headers`, `${VAR}` 확장)이
`[[plugins]]`에 필드 단위로 매핑됩니다. 두 소스는 병합되며, 이름 충돌 시
`roach-code.toml`이 이깁니다.

```json
{
  "mcpServers": {
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"] },
    "stripe": { "type": "http", "url": "https://mcp.stripe.com", "headers": { "Authorization": "Bearer ${STRIPE_KEY}" } }
  }
}
```

### 슬래시 명령

`roach-code chat`에서 내장 명령(`/compact`, `/new`, `/rewind`, `/tree`, `/branch`,
`/switch`, `/todo`, `/model`, `/effort`, `/mcp`, `/memory`, `/help`)은 로컬에서
실행됩니다. `/tree`는 저장된 대화 브랜치를 보여주고, `/branch [name]`은 현재 대화
끝에서 분기하며, `/branch <turn> [name]`은 이전 체크포인트 턴에서 분기하고,
`/switch <id|name>`은 다른 브랜치를 불러옵니다. **사용자 정의 명령**은
`.roach-code/commands/`(프로젝트) 또는 `~/.config/roach-code/commands/`(사용자)
아래의 Markdown 파일입니다 — `review.md`는 `/review`가 되고, 하위 디렉터리는
네임스페이스가 됩니다(`git/commit.md` → `/git:commit`). 본문은 프롬프트 템플릿이며,
명령을 호출하면 한 턴으로 전송됩니다.

```markdown
---
description: Review the staged diff
argument-hint: [focus-area]
---
Review the staged diff. Focus on $ARGUMENTS, list bugs with file:line.
```

`$ARGUMENTS`는 공백으로 구분된 모든 인자로, `$1`…`$N`은 위치 인자로 확장됩니다.
MCP prompts도 여기에 `/mcp__<server>__<prompt>`로 나타납니다.

### @ 참조

메시지에 `@` 참조를 넣으면 Roach Code가 전송 전에 태그된 컨텍스트 블록으로
해석합니다: `@path/to/file`(또는 `@dir`)는 로컬 파일 내용(또는 디렉터리 목록)을
주입하고, `@<server>:<uri>`는 MCP 리소스를 주입합니다. 로컬 경로는 실제로 존재할
때만 참조로 취급되므로, 일반적인 `@멘션`은 그대로 문자열로 남습니다. `/`나 `@`를
입력하면 자동완성 메뉴가 열립니다 — 슬래시 명령, 또는 계층적 파일 탐색(한 번에 한
디렉터리 레벨씩, 폴더로 진입)과 MCP 리소스.

### 두 모델 협업 (선택)

`roach-code setup`은 첫 실행을 최소화합니다: provider 선택 → 키 입력(선택한
provider의 모든 SKU가 활성화됨). 두 모델을 함께 돌리는 것(실행자 + 플래너, 캐시가
안정적인 별도 세션)은 이후 한 줄 편집으로 됩니다 — `planner_model`을 활성화된 다른
provider로 설정하세요:

```toml
[agent]
planner_model = "deepseek-pro"   # 저빈도 플래너로 사용
```

Subagent 스킬은 기본적으로 실행자 모델을 상속합니다. 다른 설정된 모델로 돌리려면
`subagent_model`을 설정하거나, `review`/`security_review` 같은 특정 스킬만 덮어쓰려면
`subagent_models`를 사용하세요.

인터랙티브 프론트엔드에서는 `agent.auto_plan = "ask"`가 복잡해 보이는 작업을 자동으로
plan 모드로 진입시킵니다: Roach Code가 먼저 읽기 전용 계획을 작성한 뒤, 편집이나
부작용이 있는 명령을 실행하기 전에 승인을 기다립니다. `auto_plan_classifier`에
`deepseek-flash` 같은 저렴한 provider를 지정할 수 있으며, 애매한 입력에서만 호출되고
분류에 실패하면 휴리스틱으로 폴백합니다.

## 아키텍처

세 단계의 확장성, 모두 코어가 이름으로 해석하는 레지스트리 뒤에 있습니다:

1. **레지스트리** — `Provider`와 `Tool`은 인터페이스이며, 코어에는 `switch model`이
   없습니다.
2. **컴파일 시점 내장** — provider(`provider/openai`)와 도구(`tool/builtin`)는
   `init()`으로 자기 등록하고, `main`이 blank-import 합니다. 내장을 추가하는 것은
   파일 하나 + import 하나입니다.
3. **런타임 플러그인** — 설정에 선언된 실행 파일로, stdin/stdout에서 줄 단위
   JSON-RPC 2.0(MCP stdio 관례)으로 통신합니다. 각 원격 도구는 `Tool` 인터페이스에
   맞춰집니다.

## 상태

**완료:** 레지스트리 기반 provider/도구, 도구 호출을 포함한 OpenAI 호환 스트리밍
(429/5xx에 대한 제한된 재시도), 내장 도구(read_file, write_file, edit_file,
multi_edit, bash, ls, glob, grep, web_fetch, task, todo_write, ask), TOML 설정,
인터랙티브 `roach-code setup` 마법사, 두 모델 협업(별도의 캐시 안정 세션에서 실행자
+ 플래너), 저빈도 컨텍스트 압축, 서브에이전트(`task`), bubbletea chat TUI(마크다운,
컨트롤러 기반 승인의 plan 모드, 실시간 토큰/활동 표시, 고정된 작업 목록, `ask` 질문
선택기, `/compact` `/new` `/tree` `/branch` `/switch` `/todo`), 세션 영속화 + 재개,
호출별 **권한**(allow/ask/deny 규칙; chat은 writer 전에 묻고, deny 규칙은 어디서나
강제 차단), 파일 writer를 프로젝트로 가두는 **워크스페이스 샌드박스**(심볼릭 링크/`..`
안전), MCP 클라이언트 — **stdio + Streamable HTTP** 전송, 도구(`mcp__server__tool`,
`readOnlyHint` 인식), prompts(슬래시 명령), resources(`@` 참조), 그리고 `/mcp`,
`[[plugins]]` 또는 프로젝트 `.mcp.json`으로 설정 — 사용자 정의 슬래시
명령(`.roach-code/commands/*.md`), `@file` / `@resource` 참조, 실행 가능한 레퍼런스
플러그인(`cmd/roach-code-plugin-example`), 하니스 루프, CLI. Wails 데스크톱
클라이언트(`desktop/`)가 같은 커널을 구동합니다.

**다음:** `bash`를 위한 OS 수준 샌드박스(macOS Seatbelt / Linux bubblewrap),
MCP OAuth + 레거시 SSE. `docs/SPEC.md` §9 참고.

<br/>

## Star History

<a href="https://www.star-history.com/?repos=tmdgusya%2Froach-code&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=tmdgusya/roach-code&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=tmdgusya/roach-code&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=tmdgusya/roach-code&type=date&legend=top-left" />
 </picture>
</a>

<br/>

<p align="center">
  <a href="https://github.com/tmdgusya/roach-code/graphs/contributors">
    <img src="https://contrib.rocks/image?repo=tmdgusya/roach-code&max=100&columns=12" alt="Contributors" width="860"/>
  </a>
</p>

<br/>

---

<p align="center">
  <sub>MIT — <a href="./LICENSE">LICENSE</a> 참고</sub>
  <br/>
  <sub>커뮤니티가 만듭니다 — <a href="https://github.com/tmdgusya/roach-code">tmdgusya/roach-code</a></sub>
</p>
