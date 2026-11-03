<p align="center">
  <img src="web/static/favicon.svg" alt="Blux" width="80" />
</p>

<h1 align="center">Blux</h1>

<p align="center">一个本地工作台，协作多个 Agent。</p>
<p align="center">
  <a href="#功能">功能</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#使用">使用</a> ·
  <a href="#开发">开发</a>
</p>

Blux 将 Codex、Claude Code、DeepSeek Harness 和 Gemini CLI 放进同一个极简界面，让你分配任务、跟踪执行、查看产物，并在确认后将代码接收到项目。

## 功能

- **多 Agent 协作**：为不同 Agent 指派独立任务，共享已完成的产物版本。
- **独立工作区**：支持任意项目目录，无需 Git 初始化或首次提交；执行前保存基线。
- **模型控制**：选择 CLI 支持的模型和推理强度，查看上报的上下文用量。
- **统一产物**：预览 Markdown、HTML、图片与源码，查看 diff、历史版本并下载文件。
- **任务管理**：流式输出、续聊、停止、置顶和删除；通过 `/` 选择技能与资料。
- **自动化**：让 Agent 按每天固定时间或手动自动执行任务，结果进收件箱，确认后才接收到项目。
- **本地持久化**：SQLite 保存任务与事件，独立 Worker 执行任务，提供备份导出和本地 MCP。

## 快速开始

### 环境要求

- Go **1.26.4+**、Node.js **22.12+**、pnpm 和 Git。
- 至少一个已安装并配置好的 Agent CLI；管家功能使用 Codex。
- 推荐 macOS。Windows 可构建运行，具体执行器受其原生平台支持限制。

Blux 复用本机 CLI 的配置与凭据。模型请求由对应 CLI 发起。

### 安装并启动

```sh
git clone https://github.com/starnighter/blux.git
cd blux

go mod download
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
node scripts/build.mjs

./bin/blux start --web-dir web/dist
```

打开 **[http://127.0.0.1:8787](http://127.0.0.1:8787)**。

Windows PowerShell 使用 `.\bin\blux.exe start --web-dir web/dist` 启动。Claude Code 的 Windows 执行需要在 WSL2 环境运行 Blux。

```sh
./bin/blux status
./bin/blux stop
```

## 使用

1. **选择项目**：选择本地文件夹，查看当前 Git 分支；没有 Git 时显示“暂无”。
2. **创建任务**：选择 Agent、模型及推理强度，输入目标开始执行。
3. **协作交接**：创建协作并指派任务，已完成文件自动汇总至公共产物区；新任务使用创建时固定的产物版本。
4. **检查结果**：在右侧工作区浏览文件、产物和代码变更，确认后点击接收。

任务在独立工作区运行，公共产物同步不会自动改写源项目。Codex 使用原生工作区沙箱、允许联网、关闭逐命令审批。本地网页默认绑定 `127.0.0.1`，面向个人使用。

### 自动化

在侧栏进入 **自动化**，保存一段提示词和一个项目，Blux 会在每天指定的时间自动运行，也可以随时点“立即运行”。

- **只在独立工作区里改**：自动化的每次运行都是一个普通任务，在独立工作区执行，不会改动你的项目。结果出现在收件箱，点开后在“变更”里确认并接收。
- **收件箱**：显示每次运行的状态（已完成 / 需要决定 / 未完成 / 已超时）和摘要。运行结束时 macOS 会弹出系统通知。Agent 遇到只有你能回答的问题时，会把问题写进 `Question.md` 并停下，运行状态显示为“需要决定”，打开任务即可回复并继续。
- **默认更保守**：命令默认**不能联网**（Codex 的工作区沙箱关闭网络；Claude Code 去掉联网工具并且不放行任何域名）。需要安装依赖或访问外部服务时，在该自动化里勾选“允许命令联网”。Agent 自己调用模型服务不受影响。
- **有上限**：单次最长 30 分钟；每个自动化每天最多运行 5 次（含手动）；连续失败 3 次会自动暂停并说明原因，修好后点“恢复”。被你停止、Worker 重启造成的中断不计入失败。
- **不抢占交互任务**：自动化最多同时占用 1 个执行槽位（`--background-concurrency`），Worker 总并发默认 3（`--concurrency`）。
- **错过的时间点**：电脑睡眠或关机期间不会运行。醒来后只补跑最近一次错过的时间点，不会连续补跑多天。
- **只支持 Codex 和 Claude Code**。

自动化依赖 Worker 持续在线。macOS 上可以安装为随登录自动启动的常驻服务：

```sh
./bin/blux service install --web-dir web/dist
./bin/blux service status
./bin/blux service uninstall
```

常驻服务由 launchd 管理，崩溃后自动重启，日志在 `~/.blux/logs/`。它不会复制你的 API Key：请先在终端里用各 CLI 自己的方式登录（如 `codex login`），Blux 只会带上 `PATH` 和 `BLUX_*_BIN` 等位置信息。安装前如果已用 `blux start` 启动了服务，请先 `blux stop`。其他系统请继续使用 `blux start`，并保持电脑开机。

### 数据与备份

数据默认保存在 `~/.blux/`；可通过 `BLUX_DATA_DIR` 或 `--data-dir` 更改。网页服务与 Worker 应使用同一数据目录。

```sh
./bin/blux stop
./bin/blux backup --output /absolute/path/blux-backup.zip
```

CLI 路径可通过 `BLUX_CODEX_BIN`、`BLUX_CLAUDE_BIN`、`BLUX_DEEPSEEK_BIN` 和 `BLUX_GEMINI_BIN` 指定。

## 开发

后端使用 Go + SQLite，前端使用 Svelte + TypeScript，并适配 Open WebUI 组件。

```sh
make setup
make dev
# http://127.0.0.1:5174
```

```sh
go test -race ./...
go vet ./...
pnpm --dir web build
```

## 致谢

前端使用 [Open WebUI](https://github.com/open-webui/open-webui) 的部分组件。来源与第三方许可证见 [UPSTREAM.md](web/UPSTREAM.md)；Agent 图标归各自权利人所有，来源见[图标说明](web/static/agents/SOURCES.md)。
