<p align="center">
  <img src="docs/logo.svg" alt="Roach Code" width="640"/>
</p>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./README.ko.md">한국어</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">规格</a>
</p>

<p align="center">
  <a href="https://github.com/tmdgusya/roach-code/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/tmdgusya/roach-code/ci.yml?style=flat-square&label=ci&labelColor=161b22&logo=githubactions&logoColor=white" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/tmdgusya/roach-code?style=flat-square&color=8b949e&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/tmdgusya/roach-code/stargazers"><img src="https://img.shields.io/github/stars/tmdgusya/roach-code.svg?style=flat-square&color=dbab09&labelColor=161b22&logo=github&logoColor=white" alt="GitHub stars"/></a>
  <a href="https://github.com/tmdgusya/roach-code/graphs/contributors"><img src="https://img.shields.io/github/contributors/tmdgusya/roach-code.svg?style=flat-square&color=bc8cff&labelColor=161b22&logo=github&logoColor=white" alt="contributors"/></a>
  <a href="https://github.com/tmdgusya/roach-code/discussions"><img src="https://img.shields.io/github/discussions/tmdgusya/roach-code.svg?style=flat-square&color=58a6ff&labelColor=161b22&logo=github&logoColor=white" alt="Discussions"/></a>
</p>

<br/>

<h3 align="center">面向终端的 AI coding agent。</h3>
<p align="center">由配置与插件驱动的极薄 harness——单一静态 Go 二进制，围绕前缀缓存调优，长会话也能把 token 成本压低。</p>

<br/>

> **Roach Code 是 [deepseek-reasonix](https://github.com/esengine/deepseek-reasonix)（作者 [@esengine](https://github.com/esengine)）的多模型 rewrite。** 它保留 Reasonix 的 harness，并从仅支持 DeepSeek 泛化到*任意* provider——Codex/OpenAI（Responses API + ChatGPT OAuth）、MiniMax、GLM（Z.ai）、Anthropic，以及任何 OpenAI 兼容端点。并非从零开始的项目，而是对上游工作的品牌重塑 + 多 provider 扩展。

<br/>

## 特性

- **配置驱动**：provider、agent、启用的工具、插件全部在 `roach-code.toml` 中声明，
  内核无硬编码模型。
- **多模型 · 可组合**：任何 OpenAI 兼容端点都只是一条配置。可选让两个模型协同
  （执行器 + 规划器），各自独立、缓存稳定的 session。
- **插件驱动**：外部工具以子进程形式运行，通过 stdio JSON-RPC 通信（MCP 兼容）；
  内置工具在编译期自注册。
- **零摩擦分发**：`CGO_ENABLED=0` 单二进制；一条命令交叉编译到六个目标平台。
  唯一依赖是一个 TOML 解析库。

## 安装

预编译二进制（无需 Go 工具链）——从最新的 GitHub release 安装：

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.sh | sh
```

```powershell
# Windows（PowerShell）
irm https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.ps1 | iex
```

安装脚本会自动识别系统/架构，对照 release 的 `SHA256SUMS` 校验下载，并把
`roach-code` 放到 PATH 上。可用 `ROACH_VERSION`、`ROACH_REPO`、`ROACH_INSTALL_DIR`
覆盖；也可直接从 [Releases](https://github.com/tmdgusya/roach-code/releases) 页面下载归档。

### 从源码构建

```sh
make build      # -> bin/roach-code
make cross      # -> dist/（darwin|linux|windows × amd64|arm64）
```

## 快速开始

```sh
roach-code setup                      # 配置向导 → ./roach-code.toml
export DEEPSEEK_API_KEY=sk-...  # 或写入 .env（见 .env.example）
roach-code chat                       # 然后在会话里运行 /init 生成 AGENTS.md（项目记忆）
roach-code run "把 main.go 里的 TODO 实现掉"
roach-code run --model mimo-pro "给这个函数补单元测试"
echo "解释这段代码" | roach-code run
```

## 配置

优先级：**flag > `./roach-code.toml` > `~/.config/roach-code/config.toml` > 内置默认值**。
密钥经环境变量通过 `api_key_env` 注入，绝不写入配置文件。

```toml
default_model = "deepseek-flash"   # 执行器；设 [agent].planner_model 可加规划器
# language    = "zh"               # 界面语言；为空则按 $LANG / $ROACH_LANG 自动检测

[agent]
# planner_model = "mimo-pro"          # 可选的低频规划器
# subagent_model = "deepseek-pro"     # runAs=subagent skill 的默认模型
# subagent_models = { review = "deepseek-pro", security_review = "deepseek-pro" }
auto_plan = "ask"                  # off|ask|on；复杂聊天任务自动进入计划模式
# auto_plan_classifier = "deepseek-flash"   # 可选；只在边界任务上调用

[[providers]]
name        = "deepseek-flash"
kind        = "openai"
base_url    = "https://api.deepseek.com"
model       = "deepseek-chat"
api_key_env = "DEEPSEEK_API_KEY"

[tools]
enabled = []   # 省略/为空 = 全部内置工具

[permissions]
mode  = "ask"                                # 无规则命中时 writer 的兜底：ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"] # 任何模式下都硬阻断
allow = ["bash(go test*)"]                   # 从不询问

[sandbox]
# workspace_root = ""          # 文件写工具被限制在此目录；留空 = 当前目录
# allow_write    = ["/tmp"]    # write_file/edit_file/multi_edit 额外可写的目录

[[plugins]]
name    = "example"
command = "roach-code-plugin-example"
```

权限逐次调用把关：`deny` > `ask` > `allow` > 兜底（只读工具永远 allow，writer 落到
`mode`）。`roach-code chat` 会在 writer 调用前征求同意（`y` 本次 · `a` 本会话 · `n` 拒绝）；
`roach-code run` 保持自主运行但仍然遵守 `deny`。完整 schema 与契约见
[`docs/SPEC.md`](docs/SPEC.md)。

权限是**策略**（哪些调用放行/询问），**沙盒**是**强制**：文件写工具
（`write_file` / `edit_file` / `multi_edit`）拒绝 `[sandbox] workspace_root`
之外的任何路径（默认当前目录，编辑不出项目），并解析符号链接与 `..`，使链接无法
打洞越界。读不受限。`bash` 本身在 macOS 默认进沙盒（`[sandbox] bash`，Seatbelt）：
命令只能写这些 root（外加临时目录与工具链缓存），`[sandbox] network` 为真时才能联网；
其它平台暂回退为不沙盒运行（越界问一次与 Linux 支持见 `docs/SPEC.md` §9）。

### 插件（MCP）

Roach Code 是一个 MCP 客户端。`[[plugins]]` 的 `type` 选择传输：`stdio`（默认）启动本地子进
程（`command`/`args`/`env`）；`http`（Streamable HTTP）连接远程 `url`，可带静态
`headers`（`${VAR}` / `${VAR:-default}` 从环境展开，密钥不入文件）。工具以
`mcp__<server>__<tool>` 暴露给模型；声明 MCP `readOnlyHint: true`
的工具会参与并行调度并命中权限层的只读默认放行。

服务器的 **prompts** 会暴露成 `/mcp__<server>__<prompt>` 斜杠命令（命令后空格分隔参
数）；**resources** 通过在消息里写 `@<server>:<uri>` 拉入；`/mcp` 列出已连接服务器及
各自暴露的内容。`make build` 还会产出 `bin/roach-code-plugin-example`——一个可直接运行的
stdio 参考实现（`echo`、`wordcount`、一个 `review` prompt、一个 style-guide 资源），
可照抄。

```toml
[[plugins]]                       # 本地 stdio 服务器
name    = "example"
command = "roach-code-plugin-example"

[[plugins]]                       # 远程 Streamable HTTP 服务器
name    = "stripe"
type    = "http"
url     = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_KEY}" }
```

**已有 `.mcp.json`？** 直接放到项目根目录，Roach Code 会原样读取——其
`mcpServers` 规范（`command`/`args`/`env`、`type`/`url`/`headers`、`${VAR}` 展开）
与 `[[plugins]]` 字段一一对应。两处来源会合并加载；同名时以 `roach-code.toml` 为准。

```json
{
  "mcpServers": {
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"] },
    "stripe": { "type": "http", "url": "https://mcp.stripe.com", "headers": { "Authorization": "Bearer ${STRIPE_KEY}" } }
  }
}
```

### 斜杠命令

`roach-code chat` 里，内置命令(`/compact`、`/new`、`/rewind`、`/tree`、`/branch`、`/switch`、`/todo`、`/model`、`/effort`、`/mcp`、`/help`)在本地执行。
`/tree` 查看已保存的对话分支，`/branch [name]` 从当前对话末端分支，`/branch <turn> [name]`
从较早的 checkpoint 轮次分支，`/switch <id|name>` 切换到另一个分支。**自定义命令**
是放在 `.roach-code/commands/`(项目)或 `~/.config/roach-code/commands/`(用户)下的 Markdown 文件——
`review.md` 即 `/review`，子目录构成命名空间(`git/commit.md` → `/git:commit`)。文件正文
是 prompt 模板，调用即作为一轮对话发出。

```markdown
---
description: Review the staged diff
argument-hint: [focus-area]
---
Review the staged diff. Focus on $ARGUMENTS, list bugs with file:line.
```

`$ARGUMENTS` 展开为全部空格分隔参数，`$1`…`$N` 为位置参数。MCP prompts 也以
`/mcp__<server>__<prompt>` 形式出现在这里。

### @ 引用

在消息里写 `@` 引用，Roach Code 会在发送前解析成带标签的上下文块：`@path/to/file`（或
`@dir`）注入本地文件内容（或目录清单），`@<server>:<uri>` 注入 MCP 资源。本地路径**只有
真实存在**时才当作引用，普通 `@mention` 保持原文。敲 `/` 或 `@` 会弹出补全菜单——斜杠
命令，或**逐层**的文件导航（一次只列当前一层目录、可下钻进子目录）外加 MCP 资源。

### 双模型协同（可选）

`roach-code setup` 刻意保持首次体验极简：选 provider → 输入 key（所选 provider 的所有
SKU 都会启用）。若要让两个模型协同（执行器 + 规划器，各自独立、缓存稳定的
session），向导后手动在 `roach-code.toml` 加一行即可：

```toml
[agent]
planner_model = "deepseek-pro"   # 作为低频规划器
```

Subagent skills 默认继承执行器模型。设置 `subagent_model` 可让它们统一走另一个已配置
模型；设置 `subagent_models` 则只覆盖 `review`、`security_review` 等指定 skill。

交互式前端中，`agent.auto_plan = "ask"` 会让看起来复杂的任务自动进入 plan
mode：Roach Code 先只读生成计划，待用户批准后才编辑文件或执行有副作用的命令。
`auto_plan_classifier` 可以指定便宜的 provider，例如 `deepseek-flash`；它只在边界输入上调用，分类失败会回退到启发式规则。

## 架构

三层可扩展性，全部藏在内核按名解析的 registry 之后：

1. **Registry**：`Provider` 与 `Tool` 是接口；内核没有 `switch model`。
2. **编译期内置**：provider（`provider/openai`）和 tool（`tool/builtin`）通过
   `init()` 自注册，`main` 用 blank import 拉入。新增内置 = 一个文件 + 一行 import。
3. **运行时插件**：配置里声明的可执行文件，通过 stdin/stdout 上的
   newline-delimited JSON-RPC 2.0（MCP stdio 约定）通信，每个远程 tool 适配成
   `Tool` 接口。

## 状态

**已完成：** 基于 registry 的 provider/tool、OpenAI 兼容流式 + 工具调用（429/5xx 有界重
试）、九个内置工具（read_file、write_file、edit_file、multi_edit、bash、ls、glob、
grep、web_fetch）、TOML 配置、交互式 `roach-code setup` 向导、双模型协同（执行器 + 规划器，
各自独立、缓存稳定的 session）、低频上下文压缩、子 agent（`task`）、bubbletea 聊天
TUI（markdown、plan mode、上下文仪表盘、`/compact` `/new` `/tree` `/branch` `/switch`）、会话持久化 + 恢复、
逐次调用**权限**（allow/ask/deny 规则；chat 在 writer 前询问，deny 在各模式硬阻断）、
**工作区沙盒**（把文件写工具限制在项目内，符号链接/`..` 安全）、
MCP 客户端——**stdio + Streamable HTTP** 传输、工具（`mcp__server__tool`，支持
`readOnlyHint`）、prompts（斜杠命令）、resources（`@` 引用）、`/mcp`，可经
`[[plugins]]` 或项目 `.mcp.json` 配置——自定义斜杠命令
（`.roach-code/commands/*.md`）、`@file` / `@resource` 引用、外加可运行的参考插件
（`cmd/roach-code-plugin-example`）、harness 主循环、CLI。chat 在终端普通缓冲区运行(原生
scrollback)并带 `/` 与 `@` 输入补全。

**后续：** 给 `bash` 套 OS 级沙盒（macOS Seatbelt / Linux bubblewrap）、MCP OAuth +
legacy SSE。见 `docs/SPEC.md` §9。

<br/>

## Star 趋势

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
    <img src="https://contrib.rocks/image?repo=tmdgusya/roach-code&max=100&columns=12" alt="贡献者" width="860"/>
  </a>
</p>

<br/>

---

<p align="center">
  <sub>MIT —— 见 <a href="./LICENSE">LICENSE</a></sub>
  <br/>
  <sub>由 <a href="https://github.com/tmdgusya/roach-code">tmdgusya/roach-code</a> 社区共建</sub>
</p>
