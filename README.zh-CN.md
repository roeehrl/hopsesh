<div align="center">

<img src="docs/assets/logo.svg" width="96" height="96" alt="hopsesh logo">

# hopsesh

**把 Claude Code 或 Codex 会话搬到另一台电脑、换另一个智能体继续，或者交给云端，之后再带回来。**

<p><a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a> | <a href="README.ja.md">日本語</a></p>

[![Release](https://img.shields.io/github/v/release/roeehrl/hopsesh)](https://github.com/roeehrl/hopsesh/releases/latest)
[![CI](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml/badge.svg)](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/roeehrl/hopsesh/badge)](https://scorecard.dev/viewer/?uri=github.com/roeehrl/hopsesh)
[![Go Reference](https://pkg.go.dev/badge/github.com/roeehrl/hopsesh.svg)](https://pkg.go.dev/github.com/roeehrl/hopsesh)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![macOS · Linux · Windows](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-0b6b62)

[安装](#install) · [快速上手](#quick-start) · [换个智能体](#continue-in-another-agent) · [往返](#round-trips) · [推送](#send-a-session-to-another-machine) · [云端会话](#cloud-sessions) · [让智能体来用](#use-it-from-your-agent) · [为什么不用…？](#why-not) · [常见问题](#faq)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hero-dark.gif">
  <img src="docs/assets/hero-light.gif" alt="hopsesh macOS 应用按仓库列出两台机器上的 Claude Code 和 Codex 会话；在计划显示哪些内容会带过去、哪些会改变、哪些会被省略之后，一个 Claude Code 会话在 Codex 中继续" width="880">
</picture>

<sub>macOS 应用，使用虚构的机器和会话录制（<a href="demo/README.md">demo/</a>）。</sub>

指南（英文）：[把 Claude Code 会话搬到另一台电脑](https://codonic.dev/guides/move-claude-code-session-to-another-computer) · [在 Codex 中继续](https://codonic.dev/guides/continue-claude-code-session-in-codex) · [移动项目目录之后](https://codonic.dev/guides/claude-code-history-after-moving-project-folder)

</div>

> 本文是英文 README 的翻译，仅供参考。**[英文 README](README.md) 是权威且最新的版本**；如有出入，以英文版为准。部分较长的参考章节在这里只做了概述，并附有指向英文原文的链接。

你在台式机上用 Claude Code 开了个头，现在换到了笔记本上，或者想让 Codex 接手。hopsesh
会列出你所有机器上每个智能体的每个会话，并把你选中的那个带到这台机器：可以留在同一个智能体里，
也可以换到另一个智能体继续，仓库、worktree 和路径都会处理好。最后它会给你一条继续会话的命令。

- **自动发现你的机器**：从 Tailscale 和 `~/.ssh/config` 中查找，只连接你允许的机器，使用你自己的
  `ssh`、密钥和 agent。读取会话不需要在对方机器上安装任何东西；没有配置 SSH 密钥的机器可以用密码登录。
- **按需在其他机器上部署自己**：`hopsesh hosts setup studio`（或应用中的 Machines › Set up hopsesh）
  会把这里的 hopsesh 复制过去，并在每次登录时于你的登录会话中启动。之后 hopsesh 就能看到那台机器上
  登录的是哪个账号（包括 Mac 的钥匙串），保护留在那里的原始会话，并在那里运行你在本机操作的会话。
  参见 [Machines](docs/machines.md)。
- **列出每个智能体的每个会话**（Claude Code 和 Codex），按仓库分组：机器、智能体、路径、分支、
  worktree、最后一条提示词、最后活动时间、是否正在运行，以及它还去过哪些地方。
- **安全地迁移会话**：在本机找到仓库或者克隆它，重建 worktree，把代码切到会话所在的提交，重写路径，
  并且事先展示计划。`hopsesh undo` 可以撤销。
- **换另一个智能体继续**：`--in codex` 或 `--in claude`，在另一台机器上或本机都可以。计划会准确列出
  哪些内容会带过去、哪些不会，以及另一个智能体将收到的交接说明（briefing）。
- **跟踪往返和分叉**：经过验证兼容的回迁只会追加新的工作。跨账号配置时，hopsesh 会创建一份可移植的
  对话，并保留原始的受保护状态。如果两份副本都有改动，它会提供一个单独的分支，绝不会悄悄合并。
- **对接各智能体的云端**：把会话交给 Claude Code on the web 或 Codex cloud（以及实验性支持的
  Copilot、Jules、Devin 或 Amp），把云端会话带回本地，还能把一个云端的会话转交给另一个云端。
  全部通过各厂商自己的命令完成，以你的身份登录。
- **在智能体内部使用**：对 Claude Code 或 Codex 说“把我笔记本上的会话拿过来”，它会先用 hopsesh
  做计划，等你同意之后才执行迁移。
- **多账号**：查看每台机器上各个工具（Claude Code、Claude 桌面版、Codex、Codex 应用）登录的账号，
  在任意装有 hopsesh 的机器上添加账号目录，用某个账号启动会话，再用另一个账号继续任何会话。
  每个会话都会显示运行它的账号。参见 [Accounts](docs/accounts.md)。
- **在 Codex 桌面应用中打开对话**：通过 Resume 菜单，直接定位到对应的线程。自定义账号根目录会使用
  终端，因为桌面链接无法选中它们。
- **CLI、TUI，以及 macOS 和 Windows 桌面应用**，共用同一个引擎，所有关键命令都支持 `--json` 输出。
  应用自带终端，也可以在 iTerm2、Terminal 或 Windows Terminal 中打开会话。应用会自动更新。

> 状态：alpha。在 macOS 和 Linux 上已可完整使用；Windows 应用和安装程序在 CI 中于 Windows Server
> 2025 上测试，包括真实窗口。hopsesh 是一个独立项目，非官方，与 Anthropic 或 OpenAI 没有任何关联。

<a id="install"></a>

## 安装

macOS 和 Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
```

两者都会用发布版本的 `checksums.txt` 及其签名校验下载内容，之后 `hopsesh update` 也会以同样的校验
安装新版本。

使用包管理器：

```sh
brew install roeehrl/tap/hopsesh            # macOS and Linux: the command
brew install --cask roeehrl/tap/hopsesh-app # macOS: the app
scoop bucket add roeehrl https://github.com/roeehrl/scoop-bucket; scoop install hopsesh   # Windows
```

通过包管理器安装的副本由包管理器负责更新（`brew upgrade`、`scoop update hopsesh`）。

- **macOS 应用：** [下载已签名并经过公证的 `.dmg`](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-macos-universal.dmg)
  （macOS 13+，Apple 芯片和 Intel 均可）。
- **Windows 应用：** [下载安装程序](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-amd64-setup.exe)
  （Windows 10/11；[arm64](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-arm64-setup.exe)）。
  它只为当前用户安装，不需要管理员权限，并会把 `hopsesh` 命令加入 PATH。安装程序目前还没有代码签名，
  所以 Windows SmartScreen 会提示“Windows 已保护你的电脑”：点击**更多信息**，再点**仍要运行**。
  [发布页面](https://github.com/roeehrl/hopsesh/releases/latest)上也有应用的便携版 `.zip`。
  在开启了**智能应用控制（Smart App Control）**的 Windows 11 上（多见于新电脑），Windows 会阻止没有
  代码签名的程序，而且没有“仍要运行”的选项。hopsesh 目前还没有代码签名，因此在签名之前无法在这类电脑上运行。
- **Linux 软件包：** amd64 和 arm64 的 `.deb`、`.rpm` 和 `.apk` 都在
  [发布页面](https://github.com/roeehrl/hopsesh/releases/latest)上，例如
  `sudo apt install ./hopsesh_<version>_linux_amd64.deb`。
- **从源码安装**（Go 1.26+）：`go install github.com/roeehrl/hopsesh/cmd/hopsesh@latest`

其他机器只需要有 SSH 服务（macOS 上是“远程登录”，Windows 上是 OpenSSH）以及上面的会话。
要换到另一个智能体继续，那个智能体必须安装在本机。

**从 0.3 升级？** 0.4 使用新格式保存设置，不会读取旧文件：应用会提议把旧文件放到一边并重新开始
（旧文件会保留在新文件旁边），命令行则会告诉你要移走哪个文件。然后重新添加并允许你的机器；
会话本身不受影响。请在每台运行 hopsesh 的机器上都更新它。

<a id="quick-start"></a>

## 快速上手

```sh
hopsesh hosts                 # machines found in Tailscale and ~/.ssh/config (connects to nothing)
hopsesh hosts allow studio    # let hopsesh connect to one
hopsesh trust studio          # confirm its SSH host key
hopsesh                       # browse sessions and move one here
```

<img src="docs/assets/demo.gif" alt="hopsesh 终端界面按仓库列出两台机器上的 Claude Code 和 Codex 会话，然后把一个 Claude Code 会话带到本机并打印继续会话的命令" width="880">

<sub>使用 <a href="https://github.com/charmbracelet/vhs">VHS</a> 根据 <a href="demo/hopsesh.tape">demo/hopsesh.tape</a> 录制。</sub>

会话的写法是 `[<machine>:][<agent>/]<id-or-title>`。省略机器名会选取任意位置上最新的副本；
标题或 id 没有歧义时，也可以省略智能体。

```sh
hopsesh agents                                 # supported agents and what's installed here
hopsesh ls --agent codex --json                # sessions of one agent, grouped by repo
hopsesh plan studio:"fix flaky tests"          # read-only preview of a move
hopsesh pull studio:"fix flaky tests"          # plan, confirm, move, print the command
hopsesh pull studio:claude/7f3c2a1e --in codex # continue a Claude Code session in Codex
hopsesh pull 7f3c2a1e                          # no machine name: bring back the newest copy
hopsesh push 7f3c2a1e laptop                   # send it to a machine that runs hopsesh
hopsesh cd 7f3c2a1e ~/git/app-2                # same session, another checkout here
hopsesh hosts add nas alice@192.168.1.20 --password   # a machine without SSH keys
hopsesh doctor studio                          # agents, SSH, host trust and the skill
hopsesh hosts status                           # hopsesh, receiving, hooks and skill on each machine
hopsesh undo                                   # undo the newest move (--list shows more)
```

### Shell 补全

`completion` 命令可以为 Bash、Zsh、Fish 和 PowerShell 生成补全脚本。在 `hopsesh` 位于 PATH 中时，
运行与当前 shell 对应的那一行，即可在本次会话中启用补全：

| Shell | 设置 |
| --- | --- |
| Bash | `source <(hopsesh completion bash)`（需要 Bash 补全支持） |
| Zsh | `autoload -Uz compinit && compinit && source <(hopsesh completion zsh)` |
| Fish | `hopsesh completion fish \| source` |
| PowerShell | `hopsesh completion powershell \| Out-String \| Invoke-Expression` |

要在新的 shell 会话中自动加载，请把对应的那一行加入 shell 启动文件（`~/.bashrc`、`~/.zshrc`、
`~/.config/fish/config.fish` 或 `$PROFILE`）。安装选项和前提条件请运行 `hopsesh completion <shell> --help` 查看。

### 桌面应用

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/app-dark.png">
  <img src="docs/assets/app-light.png" alt="hopsesh 应用按仓库列出两台机器上的 Claude Code 和 Codex 会话，显示实时状态和最后一条提示词" width="900">
</picture>

应用和命令行使用同一个引擎，每次迁移都有一张计划表：仓库、worktree 模式、哪些内容会被重写、
哪些会留在另一台机器上。会话可以按仓库、机器、智能体、状态或时间分组，并用可见的筛选标签过滤。
每个会话有一行操作：**Resume in ‹place›**、**Move**（换智能体继续、发送到其他机器、交给云端）和 **⋯**。
发送时会在对方机器上做计划并执行，一次 Undo 就能同时撤销两边。应用在 macOS 和 Windows 上相同，
会自动更新，并在安装前校验发布版本的签名和校验和。更多细节见[英文原文](README.md#the-app)。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/windows-app-dark.png">
  <img src="docs/assets/windows-app-light.png" alt="Windows 上的 hopsesh 应用按仓库列出本机和 Linux 机器 studio 上的 Claude Code 和 Codex 会话" width="900">
</picture>

### hopsesh Terminal 与你自己的终端

应用自带一个带标签页的终端 **hopsesh Terminal**，可以停靠在会话列表下方或旁边，也可以单独成窗。
会话以及 Claude Code 需要终端才能完成的步骤（交给云端、带回会话、云端登录）都可以在这里运行。
hopsesh 只负责启动命令，所有输入都由你自己完成，它从不替你回答任何问题（包括 Claude Code 的信任提示），
标签页里显示的内容也不会写入磁盘或日志。你也可以选择在自己的终端应用中打开（macOS 上是 iTerm2 或
Terminal，Windows 上是 Windows Terminal）：`hopsesh open <session>` 和 `hopsesh terminals`。
详见英文原文的 [The hopsesh Terminal](README.md#the-hopsesh-terminal) 和
[Your terminal app](README.md#your-terminal-app)。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/terminal-dark.png">
  <img src="docs/assets/terminal-light.png" alt="停靠在会话列表下方的 hopsesh Terminal：一个标签页中 Claude Code 询问是否信任 hopsesh 的交接目录，并注明 hopsesh 从不替你回答；另一个标签页运行着一个已恢复的会话" width="900">
</picture>

<a id="continue-in-another-agent"></a>

## 换另一个智能体继续

```sh
hopsesh plan studio:"fix flaky tests" --in codex   # see what carries over, change nothing
hopsesh pull studio:"fix flaky tests" --in codex   # then do it
```

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/plan-dark.png">
  <img src="docs/assets/plan-light.png" alt="在 Codex 中继续一个 Claude Code 会话的计划：带过去的、改变的和省略的内容，Codex 收到的交接说明，以及仓库和代码如何匹配" width="900">
</picture>

hopsesh 会把对话转换成另一个智能体自己的会话格式，在另一台机器上或本机都可以：

- **`--fidelity history`**（默认）以文本形式带上对话。工具活动会标注为前一个智能体的操作，较长的输出会被
  缩短，历史很长时最早的步骤会被概括。
- **`--fidelity note`** 只带一份交接说明。
- **`--native`**（实验性）在可行时，把原来的 shell 调用原样重放为目标智能体自己的调用。
- **`--via import`** 让 Codex 自带的导入器转换 Claude Code 会话；hopsesh 会补上交接说明，并保持可撤销。

每份计划都会显示一份**损失报告**（保留了什么、缩短或概括了什么、映射了哪些路径；来自另一家厂商的推理内容
永远不会带过去）。它还会显示另一个智能体收到的**交接说明**：会话来自哪里、首先要核实什么（git status、
迁移时的提交）、未完成的计划，以及只有前一个智能体读过的指令文件。`--note-file` 可以附上你自己的交接备注，
`--carry-rules` 会带上你对所有项目的通用指令，`--go` 会以 “Continue.” 启动会话。除非你启动会话，
否则不会向任何模型发送内容。详见 [docs/design.md §8](docs/design.md#8-continuing-in-another-agent)。

<a id="round-trips"></a>

## 往返

把会话搬到笔记本上，或者搬进 Codex，继续工作，之后再把它带回来：

- **标题保持不变。** hopsesh 从不为了标明去向而重命名会话：留下的副本和新副本都保留原来的标题。
  去向会显示在 hopsesh 自己的列表、TUI 和应用中（`◆ Moved out · moved to Codex on <machine>`、
  `● Moved copy`、`↩ Returned`）。
- **原始会话等你回来。** 默认情况下，留下的副本会拒绝新的提示词，直到你把会话搬回来，这样两份副本不会
  各自走远，搬回来时也能干净地回归。你可以在设置里改为“Shows a warning”或“Is left alone”，或者在警告
  之后解除某个原始会话的锁定（`hopsesh unblock`）。Codex 会要求你批准一次 hopsesh 的 hook（`/hooks`）；
  需要等待时 hopsesh 会提示你。
- **每个会话一行。** 列表会把同一会话在不同机器和智能体上的副本合并成一行。`hopsesh pull <id-or-title>`
  不带机器名时，会把最新的副本带回它原来所在的目录。
- **回到原来的智能体时只追加新工作。** 原始会话自己的轮次，包括 Claude Code 带签名的推理内容，都逐字节保持不变。
- **从不合并。** 如果两份副本都有改动，迁移会停下，直到你选择 `--keep-both`（作为单独的会话）或
  `--replace`（把本机的副本放到一边；`hopsesh undo` 可以恢复）。
- **代码会跟着走。** hopsesh 会获取会话所在的提交，如果它从未推送过，就直接从另一台机器获取，并对干净的
  检出做快进（fast-forward）。它从不 merge、rebase 或 stash；做不到时会准确说明缺了什么。
- **历史随会话一起走。** 会话旁边有一个小文件 `<session>.hopsesh.json`，记录它的副本和每一跳，因此下一次
  迁移可以在任何机器上进行。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/round-trip-dark.png">
  <img src="docs/assets/round-trip-light.png" alt="在 Codex 中继续 Find the codeword 之后：原始会话被标记为 Moved out，并在搬回之前被锁定，旁边有 Open moved copy 和 Remove block；列表显示最新的副本在 Codex 中" width="900">
</picture>

详见 [docs/design.md §10](docs/design.md#10-round-trips-and-lineage) 和
[§12](docs/design.md#12-code-follows-the-conversation)。

<a id="send-a-session-to-another-machine"></a>

## 把会话发送到另一台机器

`pull` 只需要对方机器上有 SSH。如果那台机器也运行着 hopsesh，你还可以从本机把会话发过去：

```sh
hopsesh receive on               # on the machine that receives (off by default)
hopsesh push 7f3c2a1e laptop     # on the machine that has the session
```

接收方会用自己的智能体和设置，基于该会话文件的只读副本来做计划；它无法读取或运行发送方上的其他任何东西。
在你确认之前什么都不会改变，在发送方执行 `hopsesh undo` 会同时撤销两台机器上的操作。两台机器会协商使用
双方都支持的最新协议；版本太旧的一方会收到更新提示。详见
[docs/design.md §11](docs/design.md#11-peers-working-with-hopsesh-on-the-other-machine)。

如果会话是在一个包含多个仓库的目录中启动的（例如 `~/git/course` 下有 `course-core` 和 `course-sdk`，
而它本身不是仓库），它会在另一台机器上的同一个目录中继续。其中的每个仓库都会在那边被找到（先找该目录，
再找你的仓库目录和常见位置），或者克隆进去：`pull --clone`、TUI 中的 `c`，或应用中的 **Clone … for me**。
计划会列出每个仓库以及它留下的东西（未推送的提交、未提交的文件），对话中的路径也会指向各自的仓库。
目录中的其他文件夹（例如 worktree）不会被带过去。

当 hopsesh 在那台机器的登录会话中运行时（`hopsesh hosts setup laptop`），会话也可以留在原地继续，
你在本机使用那台机器上的终端：

```sh
hopsesh terminals run studio --resume 7f3c2a1e   # continue it on studio, work in it here
hopsesh terminals run studio --dir ~/git/app     # a new session there
hopsesh doctor --all-machines                     # hopsesh, hooks and skill on every machine
```

### 每台机器能做什么

`hopsesh hosts status` 会检查每台机器：是否安装了 hopsesh 及其版本是否最新、是否在登录会话中运行并接收
会话、命令是否在 PATH 中，以及每个智能体目录中的 hook 和 skill。它会为每项检查打印修复命令，当有机器需要处理
时以退出码 3 结束。在应用中，**Settings › Machines** 汇总了这一切，**Up next** 只列出剩下要做的事以及对应的修复。
详见[英文原文](README.md#what-each-machine-can-do)。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/machines-dark.png">
  <img src="docs/assets/machines-light.png" alt="hopsesh 应用中的 Settings › Machines：This Mac 和 studio 已全部就绪并接收会话；选中的 laptop 显示为灰色空心并带有向上箭头，它的 Up next 列出把 hopsesh 从 0.6.3 更新到 0.6.4、开启接收，以及在三个智能体目录之一中设置 skill；nas 没有安装 hopsesh，只能通过 SSH 访问" width="900">
</picture>

## 换个目录，同一个会话

`hopsesh cd <session> <dir>` 让会话在同一台机器上的另一个目录（同一仓库的另一个 worktree 或克隆）中继续，
并保持为同一个会话：相同的 id，每条消息、工具调用和推理块都保持智能体写下时的样子。在应用中是会话 ⋯ 菜单里的
**Change folder…**。`hopsesh undo` 可以撤销。Claude Code 和 Codex 各有各的处理方式，详见
[英文原文](README.md#another-folder-same-session)。

<a id="cloud-sessions"></a>

## 云端会话

hopsesh 对接各智能体自己的云端：Claude Code on the web 和 Codex cloud，以及实验性支持的 GitHub Copilot
cloud agent、Jules、Devin 和 Amp。它可以把本机（或你的其他机器）上的会话交给云端，把云端会话带回本地的
Claude Code 或 Codex，还能经由本机把一个云端的会话转交给另一家厂商的云端。它不会取代厂商自己的命令，而是在本机
以你的身份运行这些命令（`claude --cloud`、`claude --teleport`、`codex cloud`、`gh agent-task`、`jules remote`、
`devin`、`amp`），并补上它们做不到的部分：来自你其他机器或另一家厂商智能体的会话、交接说明和损失报告、分支、
来源记录和撤销。它启动的云端会话属于你，计入你的套餐；hopsesh 从不读取登录凭据，也不调用厂商的服务器。
每个云端在你允许之前都是关闭的。

```sh
hopsesh clouds                                        # the clouds, and what a read-only look found
hopsesh clouds allow claude-cloud                     # each is off until you allow it
hopsesh clouds test claude-cloud                      # read-only: the login and the commands hopsesh uses
hopsesh plan claude/<id> --to codex-cloud --env acme-api   # what goes and what stays; changes nothing
hopsesh handoff claude/<id> --to codex-cloud --env acme-api
hopsesh ls --cloud                                    # cloud sessions hopsesh knows of
hopsesh pull claude-cloud:session_01… --run           # bring one home (or paste its link); --in codex
hopsesh handoff claude-cloud:session_01… --to codex-cloud --env acme-api   # cloud to cloud
hopsesh clouds cleanup                                # branches whose work is merged, deleted when you say so
```

在应用中：侧边栏的 **Clouds**、会话上的 **Move → Hand off to ‹cloud›…**（云端会话上也有）、云端会话上的
**Bring to this Mac…**，以及 **Machines** 下每个云端的卡片（Turn on、Test 和 **Sign in**，后者会在一个不做任何
记录的终端标签页中运行厂商自己的登录命令）。在终端界面中，`c` 用于交接，`enter` 把云端会话带到本机。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/handoff-dark.png">
  <img src="docs/assets/handoff-light.png" alt="把一个 Claude Code 会话交给 Claude Code cloud：计划显示带过去的、改变的和留下的内容，云端智能体收到的交接说明，hopsesh 推送的分支，以及留在这台 Mac 上的文件" width="900">
</picture>

### 上传的内容：交接说明和代码

没有哪家厂商的云端能接收一段对话，所以云端会话从一份**交接说明**开始：大约 2,000 个 token，内容包括已经做了
什么、还有什么未完成，以及你最近的要求。它由 hopsesh 生成（从不由模型生成），疑似密钥会被遮盖。工具调用、工具输出
和隐藏的推理内容都留在本机。计划会显示这份交接说明，你也可以编辑它。

代码放在云端会克隆的一个分支上。干净且已经在 GitHub 上的分支会原样使用（会话被带回时所在的云端自身分支也一样）。
否则 hopsesh 会推送一个 `hopsesh/handoff/<date>-<id>` 分支，其中是未推送提交和已修改文件的快照，不会动你的检出、
暂存区或分支。Codex cloud 可以改为把已推送分支上的少量改动作为起始 diff（`--starting-diff`），Claude Code 可以把
不在 GitHub 上的仓库作为自己的上传（`--bundle`；这样云端就无法把工作推送回来）。Jules、Devin 和 Amp 无法指定从
哪个分支开始，所以它们的交接说明会请云端智能体先检出该分支。

### 带回来的内容

| 云端 | 状态 | 方式 | 对话如何带回 | 代码如何带回 |
|---|---|---|---|---|
| Claude Code cloud | 已支持 | `claude --teleport`，在终端中 | 完整带回，作为 Claude Code 自己的副本 | 它的 `claude/…` 分支，在本机保存为 `hopsesh/from/claude-cloud/…` |
| Codex cloud（旧版任务） | 已支持 | `codex cloud status`、`codex cloud diff` | 任务标题及其结果，写成一个会话 | 它的 diff，提交在 `hopsesh/from/codex-cloud/<id>` 上 |
| GitHub Copilot cloud agent | 实验性 | `gh agent-task view --log` | 会话日志，以文本形式 | 它的 pull request 分支 |
| Amp | 实验性 | `amp threads markdown` | 它的线程，以文本形式 | 留在 orb 中 |
| Jules | 实验性 | `jules remote pull` | 留在 Jules 中 | 它的补丁，提交在 `hopsesh/from/jules/<id>` 上 |
| Devin | 实验性 | `devin list` | 留在 Devin 中 | 它的 pull request 分支 |

**实验性**意味着尚未验证：hopsesh 通过这四家厂商的命令行来驱动它们，而这些命令行的输出大多没有文档，目前只针对
它们的替身（stand-in）做过测试，没有针对真实服务测试。可能会有不完善之处，欢迎反馈你发现的问题。

代码总是进入你检出旁边的一个新 worktree，你的检出保持原样。文本会写成交接来源那个智能体的新会话（否则是
Claude Code；`--in codex` 选择 Codex），或者在使用 `--append` 且原会话保持你离开时的状态时，追加到原会话中。
目前只支持较早的 Codex cloud 环境：codex 命令暂时还无法使用在如今的 Codex cloud（chatgpt.com）中创建的环境。
Codex cloud 的每个任务都运行在你在网页上创建的**环境**中：用 `--env` 指定；计划会推荐你最近任务用过的环境，
hopsesh 也会为该仓库记住你的选择（`hopsesh clouds env codex-cloud`）。

### Claude Code 在终端中的步骤

Claude Code 只在你能作答的终端中启动云端会话或把会话复制回本地，所以这两步都在你看得见的终端里运行：应用的
hopsesh Terminal 标签页（或你在 Settings → Terminal 中选择的终端应用），或终端界面自己的终端。第一次时，
Claude Code 会询问你是否信任 hopsesh 的交接目录：每个仓库由你回答一次，hopsesh 从不替你回答，也不会修改
Claude Code 的设置来跳过它。**Claude Code 只有在你在副本中发送一条消息之后才会保存它**：发一条（哪怕只是 “ok”），
hopsesh 就会接手这份副本。详见[英文原文](README.md#claude-codes-steps-in-your-terminal)。

### 云端到云端

云端会话可以继续转交给另一家厂商的云端。没有哪家云端能直接交给另一家，所以 hopsesh 先把它带回本机（副本留在本机，
对 Claude Code 来说是完整的），再把这份副本交出去：`hopsesh handoff claude-cloud:<id> --to codex-cloud` 会从原样的
`claude/…` 分支启动 Codex cloud 任务，`hopsesh handoff codex-cloud:<id> --to claude-cloud` 会在本机提交任务的 diff，
并推送到一个交接分支供 Claude Code 使用。计划会显示两段行程以及这一趟会损失什么；副本的来源记录会记下两段；
一次撤销就能把两段都撤回。

### 撤销和分支清理

`hopsesh undo` 会撤回一次交接在本机所做的事：交接分支（仅在云端还没有向它推送、且你没有选择保留时）以及交接记录。
云端会话本身仍留在厂商的列表中，需要你在那里归档：没有哪家厂商的命令行能归档会话。撤销一次带回操作会删除它创建的
worktree、副本和分支。交接的工作合并进默认分支之后，`hopsesh clouds cleanup`（或应用中的
**Activity → Look for merged branches**）会提议删除交接分支以及云端自己的 `claude/…` 或 `copilot/…` 分支：
它以只读方式询问远端，只删除你确认的分支。在 `[clouds.<name>]` 下设置 `delete_branch = "never"` 或
`"on-undo"` 可以保留这些分支。

### 隐私

- 云端只拿到交接说明和分支，别无其他。类似凭据的文件（`.env*`、`*.tfvars`、`id_rsa*` 及其他 SSH 密钥、`*.pem`、
  `*.key`、`*.p12`、`.npmrc`）、Git LFS 文件以及超过 50 MB 的文件，无论你怎么选都不会上传。未跟踪的文件只有在
  你明确指定时才会上传（`--untracked docs/plan.md`）。
- 对话本身只有在你要求时才会放到分支上（`--history-file`，会提交 `.hopsesh/handoff.md`）：能看到该分支的任何人都能读到它。
- 密钥遮盖是尽力而为，并不保证完全；计划会显示遮盖了哪些内容。
- 云端会话中保存的内容受该厂商条款约束。这些命令的输出大多没有文档：hopsesh 会谨慎地解析。它针对每个工具的替身
  都做过测试；Claude Code cloud 和 Codex cloud 的部分功能还手动对照真实工具检查过，实验性的云端还没有。

<a id="use-it-from-your-agent"></a>

## 在智能体中使用

```sh
hopsesh skill install              # your agent asks before each hopsesh command
hopsesh skill install --add-rules  # read-only hopsesh commands run without asking
```

也可以在智能体内部以插件形式添加这个 skill（仍然需要上面的 `hopsesh` 命令；用了 `hopsesh skill install`
就不需要插件了）：

```sh
/plugin marketplace add roeehrl/hopsesh      # Claude Code, then /plugin install hopsesh@hopsesh
codex plugin marketplace add roeehrl/hopsesh # Codex
npx skills add roeehrl/hopsesh               # any agent that reads Agent Skills
```

这个 skill 会以相同的文件安装到每个已安装的智能体（Claude Code 和 Codex）中。之后你可以问这样的问题：
“把我笔记本上的会话拿过来”、“在 Codex 里继续这个”或者“studio 上有哪些会话在运行？”。智能体会先做计划，
**只有在你同意之后才会迁移**。`--add-rules` 让只读命令（`ls`、`show`、`plan`、`agents`、`clouds`、`doctor` 等）
无需询问即可运行，而 `pull`、`push`、`handoff`、`followup` 和 `undo` **始终会询问**，即使在自动模式下也是如此。
“把这个交给 Claude Code cloud”（或 Codex cloud、Copilot、Jules、Devin、Amp）也是一样：智能体会展示计划和
留下的内容，询问 Codex cloud 所需的环境，在你同意后再交接（交给 Claude Code cloud 时，它会给你一条在自己终端里
运行的命令，由 Claude Code 在那里启动会话）。这个 skill 从不启动其他智能体，也从不处理密码：对于用密码登录的机器，
智能体会请你自己运行 `hopsesh hosts setup-key`。`hopsesh skill remove` 可以把它移除。详见
[docs/design.md §13](docs/design.md#13-the-hopsesh-skill-for-every-agent) 和
[§14](docs/design.md#14-the-command-line-tool-from-the-app)。

## 工作原理

hopsesh 通过 `tailscale status --json` 和 `~/.ssh/config` 发现机器，用你系统自带的 `ssh` 连接并列出会话；
迁移时先生成计划，把会话文件复制到暂存目录，一次性重写路径（id、签名和加密内容都不会被改动），校验后安装到智能体
查找的位置，并保留撤销日志。换智能体继续时，会话先被读成一种中立的形式，再写成另一个智能体的格式。详见英文原文的
[How it works](README.md#how-it-works) 和 [docs/design.md](docs/design.md)。

### 如何对待你的数据

- **只在你自己的机器之间。** 会话通过你自己的 SSH 传输。没有 hopsesh 服务器，没有中继，也没有遥测。只有在你交接时，
  会话才会去往厂商的云端。应用可以每天检查一次 GitHub 上的新版本，前提是你同意。
- **在你说“开始”之前一切只读。** 每台机器都需要你的许可，每次迁移都会先以计划的形式展示。
- **从不迁移凭据。** 登录信息、密钥、活动的套接字和账号数据都留在原处；每台机器各自保持登录。机器的 SSH 密码保存在
  macOS 钥匙串或 Windows 凭据管理器中，或者每次都询问，永远不会写入 hopsesh 的文件或命令行。
- **对话记录里可能有密钥。** hopsesh 在迁移时会扫描疑似密钥，并可以对副本做脱敏（`--redact`）。每一个远程操作都会
  记入本地审计日志。
- **只有你的智能体会和它们的模型通信。** hopsesh 从不调用 Anthropic 或 OpenAI 的 API。
- **只通过你的智能体访问云端。** 访问厂商的云端只能通过该智能体自己的命令、以你的身份登录，并且只在你允许之后；
  hopsesh 从不读取其登录信息，也不调用其服务器。一次交接只发送交接说明（密钥已遮盖，尽力而为）和一个分支；类似凭据的
  文件从不上传，快照提交中除了一个不透明的 id 之外不包含任何信息。

<a id="why-not"></a>

## 为什么不用…？

- **Codex 自带的 Claude Code 会话导入？** 它只在一台机器上转换。hopsesh 在你需要时会使用它（`--via import`），
  并补上多机器、仓库、损失报告、交接说明，以及一条保持原始会话完整的回程。
- **Claude Code 在所有设备上的会话列表？** 你从另一台设备打开的会话，仍然运行在它启动的那台机器上，用的是那台机器的
  文件。hopsesh 则会把它搬过来：在你所在的地方、在你那份仓库中继续，之后还能搬回去。
- **Codex 应用的“交接到 SSH 主机”？** 它把一个 Codex 对话及其 Git 状态迁移到一台已连接、已保存项目的主机上。
  hopsesh 对 Claude Code 和 Codex 一视同仁，可以在你的任意机器之间迁移、换到另一个智能体，以及与云端之间来回。
- **Remote Control？** Remote Control 让你远程操控一个仍在原处运行的会话。hopsesh 则迁移会话本身，让它在你所在的
  机器上、用那台机器的文件和工具继续。它也可以为迁移后的会话开启 Remote Control。
- **复制 `.jsonl` 然后运行 `claude --resume`？** 只有在两台机器上所有路径都相同、项目目录名符合 Claude Code 的命名规则、
  并且没有第二份会话副本时才行得通。hopsesh 处理这些情况，外加仓库、worktree、附属文件和撤销。
- **在机器之间同步整个 `~/.claude`？** 同步工具会一直复制所有东西。hopsesh 只在你选择时迁移你选的那个会话，
  并为新机器做好调整。

思路不同的相关项目：
- [parcels](https://github.com/0xSero/parcels) 通过 Tailscale 把仓库连同一个运行中的智能体会话一起发送。
- [go-claude-teleport](https://github.com/mithro/go-claude-teleport) 通过 SSH 迁移会话、它的 git worktree 和 tmux 窗口。
- [claude-nomad](https://github.com/funkadelic/claude-nomad) 同步你的配置和历史，并做路径重映射。
- [chronicle](https://github.com/geekmuse/chronicle) 通过 git 同步历史。
- [sessport](https://www.npmjs.com/package/sessport) 在一台机器上于不同智能体之间转换会话。

<a id="faq"></a>

## 常见问题

<details>
<summary><b>会有东西离开我的机器吗？</b></summary>

只有你交给云端的内容。会话通过 SSH 直接在你的机器之间传输。hopsesh 没有服务器、没有遥测，也从不调用 Anthropic 或
OpenAI 的 API。一次交接会通过你所选厂商自己的命令、以你的身份，把交接说明和一个分支发送到该云端
（参见[云端会话](#cloud-sessions)）。
</details>

<details>
<summary><b>需要 Tailscale 吗？</b></summary>

不需要。任何你能 `ssh` 过去的机器都可以，包括 `~/.ssh/config` 中的别名、ProxyJump 和 ProxyCommand。Tailscale 只是
让机器更容易被找到。当某个别名的局域网名称无法解析时，hopsesh 会回退到该机器的 Tailscale 名称。
</details>

<details>
<summary><b>我的某台机器用密码登录，而不是 SSH 密钥</b></summary>

用 `hopsesh hosts add <name> <destination> --password` 添加它，或者用 `hopsesh hosts auth <machine> password`
切换已有的机器。在应用中，使用 Machines 界面上的 “Login” 按钮，或在添加时勾选 “This machine logs in with a password”。

hopsesh 在连接时会询问密码：
- **在 macOS 和 Windows 上**，默认会记在钥匙串或 Windows 凭据管理器中（`--keychain=false` 则每次询问）。
- **其他情况下**，在终端中用隐藏输入提示，或在应用中弹出对话框。
- **在脚本中**，`--password-stdin` 从标准输入读取。

密码永远不会写入 hopsesh 的文件，也不会出现在命令行中。

更好的做法是 `hopsesh hosts setup-key <machine>`（应用中为 “Set up key login”）：用密码登录一次，把你的 SSH 密钥添加
过去，然后确认密钥登录可用，把该机器切换为密钥登录并忘掉密码。适用于 macOS 和 Linux 机器。`hopsesh hosts` 会显示每台
机器的登录方式。详见 [docs/design.md §16](docs/design.md#16-password-login)。
</details>

<details>
<summary><b>另一台机器需要安装 hopsesh 吗？</b></summary>

不需要。`pull` 只需要那边有 SSH 服务。只有当你要从本机 `push` 到那台机器时，才需要那边装有 hopsesh（并且运行了
`hopsesh receive on`）。
</details>

<details>
<summary><b>换到另一个智能体继续时，会丢失什么？</b></summary>

在写入任何东西之前，计划都会告诉你。通常对话会以文本形式到达，工具调用会显示为前一个智能体的活动，较长的输出会被缩短，
来自另一家厂商的推理内容会被省略。当你把会话带回来时，只有新的工作会追加到原始会话中，所以原始会话中的内容不会丢失。
</details>

<details>
<summary><b>如果会话还在另一台机器上运行怎么办？</b></summary>

hopsesh 会按原样复制并完成交接：之后在留下的副本中产生的消息会留在那里，而原始会话会被锁定，直到你搬回来。hopsesh
拒绝替换本机上正在打开的会话，除非用 `--stop-local` 先退出它。对 Codex 来说，这只能在两轮之间进行，因为 Codex 没有
在一轮中途优雅停止的方式：如果它正在工作，hopsesh 会请你等它完成。列表中运行中的会话显示为 “live working” 或
“live idle”。
</details>

<details>
<summary><b>我迁移了一个会话，但也继续用了旧副本，会怎样？</b></summary>

再次迁移时会停下并说明：两份副本都有改动，而 hopsesh 从不合并它们。选择 `--keep-both` 把传入的副本作为单独的会话带进来，
或者选择 `--replace` 把本机的副本放到一边（`hopsesh undo` 可以恢复）。
</details>

<details>
<summary><b>用户名、主目录或操作系统不同怎么办？</b></summary>

这正是它要解决的问题。路径会针对新机器重写，包括 JSON 转义的 Windows 路径，分隔符也会在 Windows 与 macOS/Linux 之间
转换。无法映射的路径会保持原样，并在计划中列出。
</details>

<details>
<summary><b>两台机器上的账号不同怎么办？</b></summary>

hopsesh 会询问每个智能体登录的是哪个账号：Claude Code 用 `claude auth status`，Codex 则询问 Codex 本身（从不读取它的
`auth.json`）。如果账号不同，它会省略只有原账号才能使用的内容：Claude Code 带签名的思考块，以及 Codex 的加密推理和
压缩（compaction）记录。这些内容会被整块移除，从不编辑，对话本身保持不变。
</details>

<details>
<summary><b>为什么 macOS 会询问“本地网络上的设备”？</b></summary>

macOS 15 及更高版本在应用访问局域网中的机器之前会请求许可。hopsesh 应用会在第一次连接这类机器时请求，并解释原因；如果你
选择了“不允许”，它会给出指向 系统设置 → 隐私与安全性 → 本地网络 的链接。Tailscale 连接不受影响，从 Terminal 运行的
命令行工具也从不需要这个权限。
</details>

<details>
<summary><b>为什么 macOS 会询问 hopsesh 是否可以控制 iTerm2 或 Terminal？</b></summary>

在另一个应用中打开标签页或把它切到前台，需要 macOS 的“自动化”权限，每个应用询问一次。hopsesh 只用它来打开运行自己启动
命令的标签页，以及选中会话所在的标签页。如果你对 iTerm2 选择了“不允许”，hopsesh 会改用 Terminal；如果 Terminal 也被拒绝，
它会给你一条可复制的命令。你之后可以在 系统设置 → 隐私与安全性 → 自动化 中更改。
</details>

<details>
<summary><b>hopsesh 会使用 iTerm2 的 Python API 吗？</b></summary>

只有在你自己开启了它的情况下（iTerm2 → Settings → General → Magic → Enable Python API）；hopsesh 从不替你开启，也从不
修改 iTerm2 或 Claude Code 的设置。开启后，交接步骤会在你当前会话旁边的分屏中打开，应用能立即知道你关闭了某个步骤的标签页，
“Show” 也能直接找到运行中会话的标签页。它每次连接时向 iTerm2 请求一次 API cookie（macOS 会询问你一次是否允许 hopsesh
控制 iTerm2），并且只保存在内存中。hopsesh 的客户端无法向会话输入内容、注入输出或读取屏幕内容：它根本没有这些请求。
API 关闭时，hopsesh 照旧使用 AppleScript。
</details>

<details>
<summary><b>如何撤销一次迁移？</b></summary>

`hopsesh undo` 撤销最近一次迁移或换智能体继续，`hopsesh undo <id>` 撤销指定的一次。它会删除新文件、恢复被替换的文件、
截掉追加的记录并找回被放到一边的副本，其他机器上的也一样。`hopsesh undo --list` 显示可以撤销的操作。
</details>

## 参与贡献

欢迎贡献。标有 [good first issue](https://github.com/roeehrl/hopsesh/contribute) 的 issue 是不错的起点。你不需要两台
机器：`demo/record.sh shell` 会在 Docker 中启动两台虚构的机器，并把你放到其中一台已经准备好 hopsesh 的机器上
（参见 [demo/README.md](demo/README.md)）。开发环境设置见 [CONTRIBUTING.md](CONTRIBUTING.md)，问题和想法请到
[Discussions](https://github.com/roeehrl/hopsesh/discussions)，安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。
想添加对新智能体的支持，请参阅英文原文的 [Add your agent](README.md#add-your-agent)。

## 许可证

[Apache License 2.0](LICENSE)。“Claude” 和 “Claude Code” 是 Anthropic, PBC 的商标；“Codex” 和 “OpenAI” 是 OpenAI 的商标。
hopsesh 与 Anthropic 或 OpenAI 没有任何关联，也未获得其认可或赞助。
