<div align="center">

<img src="docs/assets/logo.svg" width="96" height="96" alt="hopsesh logo">

# hopsesh

**Claude Code や Codex のセッションを別のコンピューターに移す、もう一方のエージェントで続ける、クラウドに引き渡す。そして、また手元に戻す。**

<p><a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a> | <a href="README.ja.md">日本語</a></p>

[![Release](https://img.shields.io/github/v/release/roeehrl/hopsesh)](https://github.com/roeehrl/hopsesh/releases/latest)
[![CI](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml/badge.svg)](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/roeehrl/hopsesh/badge)](https://scorecard.dev/viewer/?uri=github.com/roeehrl/hopsesh)
[![Go Reference](https://pkg.go.dev/badge/github.com/roeehrl/hopsesh.svg)](https://pkg.go.dev/github.com/roeehrl/hopsesh)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![macOS · Linux · Windows](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-0b6b62)

[インストール](#install) · [クイックスタート](#quick-start) · [別のエージェントへ](#continue-in-another-agent) · [往復](#round-trips) · [プッシュ](#send-a-session-to-another-machine) · [クラウドセッション](#cloud-sessions) · [エージェントから使う](#use-it-from-your-agent) · [なぜ…ではないのか](#why-not) · [FAQ](#faq)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hero-dark.gif">
  <img src="docs/assets/hero-light.gif" alt="hopsesh の macOS アプリが 2 台のマシンの Claude Code と Codex のセッションをリポジトリごとに一覧表示し、引き継がれるもの・変わるもの・除外されるものを示すプランの後、Claude Code のセッションを Codex で続ける様子" width="880">
</picture>

<sub>macOS アプリ。架空のマシンとセッションで録画しています（<a href="demo/README.md">demo/</a>）。</sub>

ガイド（英語）：[Claude Code のセッションを別のコンピューターに移す](https://codonic.dev/guides/move-claude-code-session-to-another-computer) · [Codex で続ける](https://codonic.dev/guides/continue-claude-code-session-in-codex) · [プロジェクトフォルダーを移動した後](https://codonic.dev/guides/claude-code-history-after-moving-project-folder)

</div>

> この文書は英語版 README の翻訳です。**[英語版 README](README.md) が正式かつ最新の版**であり、内容に違いがある場合は英語版が優先されます。長いリファレンス的な節は要約のみとし、英語版の該当箇所へのリンクを付けています。

デスクトップの Claude Code で始めた作業を、いまはノートパソコンで続けたい。あるいは Codex に引き継ぎたい。
hopsesh は、すべてのマシン上にあるすべてのエージェントのセッションを一覧表示し、選んだものをこのマシンに持ってきます。
同じエージェントのままでも、もう一方のエージェントに切り替えても構いません。リポジトリ、ワークツリー、パスはきちんと
整えられ、最後に作業を再開するためのコマンドが表示されます。

- **マシンを見つける**：Tailscale と `~/.ssh/config` から探し、許可したマシンにだけ、あなた自身の `ssh`、鍵、
  エージェントで接続します。読み取りだけなら相手側に何もインストールする必要はありません。SSH 鍵のないマシンには
  パスワードでログインできます。
- **頼めば他のマシンにも自分をセットアップ**：`hopsesh hosts setup studio`（またはアプリの Machines › Set up hopsesh）で、
  この hopsesh を相手側にコピーし、ログインのたびにログインセッション内で起動させます。以後 hopsesh は、そこでどの
  アカウントがサインインしているか（Mac のキーチェーンを含む）を把握し、そこに残したオリジナルを保護し、手元で操作する
  セッションをそこで実行できます。詳しくは [Machines](docs/machines.md) を参照してください。
- **すべてのエージェントのすべてのセッションを一覧表示**（Claude Code と Codex）。リポジトリ別に、マシン、エージェント、
  パス、ブランチ、ワークツリー、最後のプロンプト、最終アクティビティ、実行中かどうか、これまでにどこにあったかを表示します。
- **セッションを安全に移動**：このマシンでリポジトリを見つけるか clone し、ワークツリーを作り直し、コードをセッションの
  コミットに合わせ、パスを書き換えます。実行前には必ずプランを表示します。`hopsesh undo` で元に戻せます。
- **別のエージェントで続ける**：`--in codex` または `--in claude`。別のマシンでもこのマシンでも可能です。プランには、
  何が引き継がれ何が引き継がれないか、そして相手のエージェントが受け取るブリーフィングが正確に表示されます。
- **往復とフォークを追跡**：互換性が確認された戻りでは、新しい作業だけが追加されます。アカウントプロファイルをまたぐ場合は
  ポータブルな会話を作り、元の保護された状態を保持します。両方のコピーが変更されていれば別ブランチを提案し、黙って
  マージすることは決してありません。
- **各エージェントのクラウドと連携**：セッションを Claude Code on the web や Codex cloud（実験的に Copilot、Jules、Devin、
  Amp も）に引き渡し、クラウドのセッションを手元に持ち帰り、あるクラウドのセッションを別のクラウドに渡すこともできます。
  すべてベンダー自身のコマンドを使い、あなたとしてサインインした状態で行います。
- **エージェントの中から使える**：Claude Code や Codex に「ノートパソコンのセッションをここに持ってきて」と頼めば、
  hopsesh でプランを立て、あなたが承認してから移動します。
- **複数アカウント**：各マシンで各ツール（Claude Code、Claude デスクトップ、Codex、Codex アプリ）がどのアカウントで
  サインインしているかを確認し、hopsesh のある任意のマシンにアカウントフォルダーを追加し、あるアカウントでセッションを
  始め、別のアカウントで続けられます。各セッションには、それを実行したアカウントが表示されます。詳しくは
  [Accounts](docs/accounts.md) を参照してください。
- **Codex の会話をデスクトップアプリで開く**：Resume メニューから、該当スレッドを選択した状態で開きます。カスタムの
  アカウントルートはデスクトップのリンクで選択できないため、ターミナルを使います。
- **CLI、TUI、macOS と Windows のデスクトップアプリ**が同じエンジンで動き、重要な箇所ではどこでも `--json` 出力に対応します。
  アプリは独自のターミナルを持ち、iTerm2、Terminal、Windows Terminal でセッションを開くこともできます。アプリは自動で
  アップデートされます。

> ステータス：アルファ版。macOS と Linux ではエンドツーエンドで動作し、Windows アプリとインストーラーは CI 上の
> Windows Server 2025 で実際のウィンドウも含めてテストされています。hopsesh は独立した非公式プロジェクトであり、
> Anthropic および OpenAI とは一切関係ありません。

<a id="install"></a>

## インストール

macOS と Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
```

どちらもダウンロードしたファイルをリリースの `checksums.txt` とその署名で検証します。以降のリリースは
`hopsesh update` が同じ検証を行ってインストールします。

パッケージマネージャーを使う場合：

```sh
brew install roeehrl/tap/hopsesh            # macOS and Linux: the command
brew install --cask roeehrl/tap/hopsesh-app # macOS: the app
scoop bucket add roeehrl https://github.com/roeehrl/scoop-bucket; scoop install hopsesh   # Windows
```

パッケージマネージャーでインストールしたものは、そのパッケージマネージャーで更新します（`brew upgrade`、`scoop update hopsesh`）。

- **macOS アプリ：** [署名・公証済みの `.dmg` をダウンロード](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-macos-universal.dmg)
  （macOS 13 以降、Apple シリコンと Intel）。
- **Windows アプリ：** [インストーラーをダウンロード](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-amd64-setup.exe)
  （Windows 10/11、[arm64](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-arm64-setup.exe)）。
  現在のユーザーにだけインストールされ、管理者権限は不要で、`hopsesh` コマンドを PATH に追加します。インストーラーはまだ
  コード署名されていないため、Windows SmartScreen が「Windows によって PC が保護されました」と表示します。**詳細情報**を
  クリックし、**実行**を選んでください。アプリのポータブル版 `.zip` は[リリースページ](https://github.com/roeehrl/hopsesh/releases/latest)にあります。
  **スマート アプリ コントロール**が有効な Windows 11（主に新しい PC）では、コード署名のないプログラムはブロックされ、
  「実行」の選択肢もありません。hopsesh はまだコード署名されていないため、署名されるまではそうした PC では動作しません。
- **Linux パッケージ：** amd64 と arm64 向けの `.deb`、`.rpm`、`.apk` が
  [リリースページ](https://github.com/roeehrl/hopsesh/releases/latest)にあります。例：
  `sudo apt install ./hopsesh_<version>_linux_amd64.deb`
- **ソースから**（Go 1.26 以降）：`go install github.com/roeehrl/hopsesh/cmd/hopsesh@latest`

他のマシンに必要なのは SSH サーバー（macOS では「リモートログイン」、Windows では OpenSSH）とセッションだけです。
別のエージェントで続けるには、そのエージェントがこのマシンにインストールされている必要があります。

**0.3 からアップグレードする場合：** 0.4 は新しい形式で設定を保存し、古いファイルは読み込みません。アプリは古いファイルを
脇に置いて新しく始めることを提案し（古いファイルは新しいファイルの隣に残ります）、コマンドラインは移動すべきファイル名を
表示します。その後、マシンを改めて追加・許可してください。セッションには影響しません。hopsesh を実行しているすべての
マシンで更新してください。

<a id="quick-start"></a>

## クイックスタート

```sh
hopsesh hosts                 # machines found in Tailscale and ~/.ssh/config (connects to nothing)
hopsesh hosts allow studio    # let hopsesh connect to one
hopsesh trust studio          # confirm its SSH host key
hopsesh                       # browse sessions and move one here
```

<img src="docs/assets/demo.gif" alt="hopsesh のターミナル UI が 2 台のマシンの Claude Code と Codex のセッションをリポジトリごとに一覧表示し、Claude Code のセッションをこのマシンに持ってきて再開コマンドを表示する様子" width="880">

<sub><a href="https://github.com/charmbracelet/vhs">VHS</a> で <a href="demo/hopsesh.tape">demo/hopsesh.tape</a> から録画。</sub>

セッションは `[<machine>:][<agent>/]<id-or-title>` の形で指定します。マシン名を省略するとどこかにある最新のコピーが選ばれ、
タイトルや id が一意であればエージェント名も省略できます。

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

### シェル補完

`completion` コマンドは Bash、Zsh、Fish、PowerShell 用のスクリプトを生成します。`hopsesh` が PATH にある状態で、
使っているシェルに対応する行を実行すると、そのシェルセッションで補完が有効になります。

| シェル | 設定 |
| --- | --- |
| Bash | `source <(hopsesh completion bash)`（Bash の補完機能が必要） |
| Zsh | `autoload -Uz compinit && compinit && source <(hopsesh completion zsh)` |
| Fish | `hopsesh completion fish \| source` |
| PowerShell | `hopsesh completion powershell \| Out-String \| Invoke-Expression` |

新しいシェルでも読み込むには、対応する行をシェルの起動ファイル（`~/.bashrc`、`~/.zshrc`、`~/.config/fish/config.fish`、
`$PROFILE`）に追加してください。インストールの選択肢や前提条件は `hopsesh completion <shell> --help` で確認できます。

### アプリ

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/app-dark.png">
  <img src="docs/assets/app-light.png" alt="hopsesh アプリが 2 台のマシンの Claude Code と Codex のセッションをリポジトリごとに、ライブの状態と最後のプロンプト付きで一覧表示している画面" width="900">
</picture>

エンジンはコマンドラインと同じで、移動のたびにプランシートが表示されます。リポジトリ、ワークツリーのモード、書き換えられる
内容、相手のマシンに残るものが確認できます。セッションはリポジトリ、マシン、エージェント、状態、経過時間でグループ化でき、
フィルターはチップとして表示されます。各セッションには **Resume in ‹place›**、**Move**（別エージェントで続ける、別マシンに
送る、クラウドに引き渡す）、**⋯** の操作が並びます。送信時は相手のマシンでプランを立てて実行し、1 回の Undo で両側を
元に戻せます。アプリは macOS と Windows で同じで、自動アップデート時にはリリースの署名とチェックサムを検証します。
詳細は[英語版](README.md#the-app)を参照してください。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/windows-app-dark.png">
  <img src="docs/assets/windows-app-light.png" alt="Windows 版 hopsesh アプリが、この PC と Linux マシン studio の Claude Code と Codex のセッションをリポジトリごとに一覧表示している画面" width="900">
</picture>

### hopsesh Terminal と、いつものターミナル

アプリにはタブ付きの独自ターミナル **hopsesh Terminal** があり、セッション一覧の下や横にドッキングするか、独立した
ウィンドウとして使えます。セッションのほか、Claude Code がターミナルを必要とする手順（クラウドへの引き渡し、セッションの
持ち帰り、クラウドへのサインイン）もここで実行できます。hopsesh はコマンドを起動するだけで、入力はすべてあなたが行い、
質問に代わりに答えることはありません（Claude Code の信頼確認も含む）。タブの表示内容がディスクやログに書き込まれることも
ありません。いつものターミナルアプリ（macOS では iTerm2 または Terminal、Windows では Windows Terminal）で開くことも
できます：`hopsesh open <session>`、`hopsesh terminals`。詳しくは英語版の
[The hopsesh Terminal](README.md#the-hopsesh-terminal) と [Your terminal app](README.md#your-terminal-app) を参照してください。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/terminal-dark.png">
  <img src="docs/assets/terminal-light.png" alt="セッション一覧の下にドッキングした hopsesh Terminal。一方のタブでは Claude Code が hopsesh の引き渡し用フォルダーを信頼するか尋ねており、hopsesh は代わりに答えないという注記がある。もう一方のタブでは再開したセッションが動いている" width="900">
</picture>

<a id="continue-in-another-agent"></a>

## 別のエージェントで続ける

```sh
hopsesh plan studio:"fix flaky tests" --in codex   # see what carries over, change nothing
hopsesh pull studio:"fix flaky tests" --in codex   # then do it
```

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/plan-dark.png">
  <img src="docs/assets/plan-light.png" alt="Claude Code のセッションを Codex で続けるためのプラン：引き継がれるもの、変わるもの、除外されるもの、Codex が受け取るブリーフィング、リポジトリとコードの合わせ方" width="900">
</picture>

hopsesh は会話を相手のエージェント自身のセッション形式に変換します。別のマシンでもこのマシンでも可能です。

- **`--fidelity history`**（デフォルト）は会話をテキストとして持ち込みます。ツールの実行は前のエージェントのものとして
  ラベル付けされ、長い出力は短縮され、履歴が長い場合は古いステップが要約されます。
- **`--fidelity note`** はブリーフィングだけを持ち込みます。
- **`--native`**（実験的）は、可能な場合にシェル呼び出しを相手エージェント自身の呼び出しとしてそのまま再現します。
- **`--via import`** は Codex 自身のインポーターで Claude Code のセッションを変換します。hopsesh はブリーフィングを追加し、
  元に戻せる状態を保ちます。

どのプランにも**損失レポート**が表示されます（保持されたもの、短縮・要約されたもの、マッピングされたパス。他ベンダーの
推理（reasoning）は決して引き継がれません）。相手のエージェントが受け取る**ブリーフィング**も表示されます：セッションの出所、
最初に確認すべきこと（git status、移動時点のコミット）、未完了のプラン、前のエージェントだけが読んでいた指示ファイル。
`--note-file` で独自の引き継ぎメモを、`--carry-rules` で全プロジェクト共通の指示を追加でき、`--go` は「Continue.」で
セッションを開始します。あなたがセッションを開始しない限り、モデルには何も送信されません。詳細：
[docs/design.md §8](docs/design.md#8-continuing-in-another-agent)。

<a id="round-trips"></a>

## 往復

セッションをノートパソコンや Codex に移して作業し、後でまた戻せます。

- **タイトルはそのまま。** hopsesh は行き先を示すためにセッション名を変えることはありません。残されたコピーも新しいコピーも
  元のタイトルを保ちます。移動の状況は hopsesh 自身の一覧、TUI、アプリに表示されます（`◆ Moved out · moved to Codex on <machine>`、
  `● Moved copy`、`↩ Returned`）。
- **オリジナルはあなたの帰りを待つ。** デフォルトでは、残されたコピーはセッションを戻すまで新しいプロンプトを受け付けません。
  そのため 2 つのコピーが食い違うことはなく、戻すときもきれいに戻せます。設定で「Shows a warning」や「Is left alone」を
  選ぶことも、警告の上で特定のオリジナルのブロックを外すこと（`hopsesh unblock`）もできます。Codex では hopsesh の
  フックを一度承認する必要があります（`/hooks`）。待機中のときは hopsesh が知らせます。
- **1 セッション 1 行。** 一覧では、マシンやエージェントをまたいだ同じセッションのコピーが 1 行にまとめられます。
  マシン名なしの `hopsesh pull <id-or-title>` は、最新のコピーを元のフォルダーに持ち帰ります。
- **元のエージェントに戻るときは新しい作業だけを追加。** 元のセッション自身のターンは、Claude Code の署名付き推理も含め、
  1 バイトも変わりません。
- **マージは決してしない。** 両方のコピーが変更されていれば、`--keep-both`（別セッションとして）か `--replace`
  （このマシンのコピーを脇に置く。`hopsesh undo` で戻せる）を選ぶまで移動は止まります。
- **コードもついてくる。** hopsesh はセッションのコミットを fetch し（push されていなければ相手のマシンから直接）、
  クリーンなチェックアウトを fast-forward します。merge、rebase、stash は一切しません。それができない場合は、何が足りないかを
  正確に伝えます。
- **履歴はセッションと一緒に移動する。** セッションの隣にある小さな `<session>.hopsesh.json` がコピーと移動の経路を
  記録するので、次の移動はどのマシンからでも行えます。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/round-trip-dark.png">
  <img src="docs/assets/round-trip-light.png" alt="Find the codeword を Codex で続けた後：オリジナルは Moved out と表示され、戻されるまでブロックされている。Open moved copy と Remove block があり、一覧には最新のコピーが Codex にあることが示されている" width="900">
</picture>

詳細：[docs/design.md §10](docs/design.md#10-round-trips-and-lineage)、
[§12](docs/design.md#12-code-follows-the-conversation)。

<a id="send-a-session-to-another-machine"></a>

## セッションを別のマシンに送る

`pull` に必要なのは相手のマシンの SSH だけです。相手のマシンでも hopsesh が動いていれば、こちらからセッションを送ることもできます。

```sh
hopsesh receive on               # on the machine that receives (off by default)
hopsesh push 7f3c2a1e laptop     # on the machine that has the session
```

受信側は、自身のエージェントと設定を使い、そのセッションのファイルの読み取り専用コピーに対してプランを立てます。送信側の
それ以外のものを読んだり実行したりすることはできません。あなたが確認するまで何も変わらず、送信側で `hopsesh undo` を実行すると
両方のマシンが元に戻ります。2 台のマシンは双方が対応する最新のプロトコルで合意し、古すぎる側にはアップデートが提案されます。
詳細：[docs/design.md §11](docs/design.md#11-peers-working-with-hopsesh-on-the-other-machine)。

複数のリポジトリを含むフォルダー（リポジトリそのものではなく、`course-core` と `course-sdk` を含む `~/git/course` など）で
始めたセッションは、相手のマシンでも同じフォルダーで続きます。中の各リポジトリは相手側で探され（まずそのフォルダー、次に
リポジトリ用フォルダーや一般的な場所）、見つからなければそこへ clone されます：`pull --clone`、TUI の `c`、アプリの
**Clone … for me**。プランには各リポジトリと、そこに残されるもの（未 push のコミット、未コミットのファイル）が列挙され、
会話内のパスもそれぞれのリポジトリを指すようになります。フォルダー内のその他のフォルダー（ワークツリーなど）は持ち込まれません。

相手のマシンのログインセッションで hopsesh が動いていれば（`hopsesh hosts setup laptop`）、セッションをその場で続けることも
できます。相手のマシン上のターミナルを手元から使う形です。

```sh
hopsesh terminals run studio --resume 7f3c2a1e   # continue it on studio, work in it here
hopsesh terminals run studio --dir ~/git/app     # a new session there
hopsesh doctor --all-machines                     # hopsesh, hooks and skill on every machine
```

### 各マシンでできること

`hopsesh hosts status` はすべてのマシンをチェックします：hopsesh がインストールされているか、そのバージョンが最新か、
ログインセッションで動いてセッションを受信しているか、コマンドが PATH にあるか、各エージェントフォルダーのフックと
スキルの状態。各チェックを直すコマンドを表示し、対応が必要なマシンがあれば終了コード 3 で終わります。アプリでは
**Settings › Machines** にすべてがまとまっており、**Up next** には残りの作業とその対処だけが並びます。詳細は
[英語版](README.md#what-each-machine-can-do)を参照してください。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/machines-dark.png">
  <img src="docs/assets/machines-light.png" alt="hopsesh アプリの Settings › Machines：This Mac と studio はすべて準備済みでセッションを受信している。選択中の laptop は灰色の白抜きで上向き矢印付き。その Up next には hopsesh を 0.6.3 から 0.6.4 へ更新、受信の有効化、3 つあるエージェントフォルダーのうち 1 つへのスキル設定が並ぶ。nas には hopsesh がなく SSH のみで接続" width="900">
</picture>

## 別のフォルダー、同じセッション

`hopsesh cd <session> <dir>` は、同じマシン上の別のフォルダー（同じリポジトリの別のワークツリーや clone）でセッションを
続け、しかも同じセッションのまま保ちます：同じ id、すべてのメッセージ、ツール呼び出し、推理ブロックがエージェントの書いた
とおりに残ります。アプリではセッションの ⋯ メニューにある **Change folder…** です。`hopsesh undo` で元に戻せます。
Claude Code と Codex ではそれぞれ処理が異なります。詳細は[英語版](README.md#another-folder-same-session)を参照してください。

<a id="cloud-sessions"></a>

## クラウドセッション

hopsesh は各エージェント自身のクラウドと連携します：Claude Code on the web と Codex cloud、そして実験的に GitHub Copilot
cloud agent、Jules、Devin、Amp。このマシン（または手持ちの別のマシン）のセッションをクラウドに引き渡し、クラウドの
セッションを手元の Claude Code や Codex に持ち帰り、このマシンを経由してあるクラウドのセッションを別ベンダーのクラウドに
渡すことができます。ベンダー自身のコマンドを置き換えるのではなく、それらを（`claude --cloud`、`claude --teleport`、
`codex cloud`、`gh agent-task`、`jules remote`、`devin`、`amp`）このマシン上で、あなたとしてサインインした状態で実行し、
それらにできないことを補います：別のマシンや他ベンダーのエージェントのセッション、ブリーフィングと損失レポート、ブランチ、
系譜（lineage）、undo。hopsesh が開始するクラウドセッションはあなたのもので、あなたのプランで動きます。hopsesh がログイン
情報を読んだりベンダーのサーバーを呼び出したりすることはありません。各クラウドは、あなたが許可するまでオフです。

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

アプリでは：サイドバーの **Clouds**、セッション上の **Move → Hand off to ‹cloud›…**（クラウドセッションでも可）、
クラウドセッション上の **Bring to this Mac…**、そして **Machines** の下にあるクラウドごとのカード（Turn on、Test、
**Sign in**。Sign in は何も記録しないターミナルタブでベンダー自身のログインコマンドを実行します）。ターミナル UI では
`c` で引き渡し、`enter` でクラウドセッションをこのマシンに持ってきます。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/handoff-dark.png">
  <img src="docs/assets/handoff-light.png" alt="Claude Code のセッションを Claude Code cloud に引き渡す：プランには引き継がれるもの、変わるもの、残るもの、クラウドのエージェントが受け取るブリーフィング、hopsesh が push するブランチ、この Mac に残るファイルが表示されている" width="900">
</picture>

### クラウドに送られるもの：ブリーフィングとコード

会話そのものを受け取れるベンダーのクラウドはないため、クラウドセッションは**ブリーフィング**から始まります。これは約
2,000 トークンで、これまでにやったこと、未完了のこと、最近の依頼をまとめたものです。hopsesh が作成し（モデルが作ることは
ありません）、秘密情報らしきものはマスクされます。ツール呼び出し、ツール出力、非表示の推理はこのマシンに残ります。
ブリーフィングはプランに表示され、編集もできます。

コードはクラウドが clone するブランチに載せます。クリーンで既に GitHub にあるブランチはそのまま使われます（セッションの
持ち帰りに使われたクラウド自身のブランチも同様）。そうでなければ hopsesh は、未 push のコミットと変更ファイルのスナップショットを
含む `hopsesh/handoff/<date>-<id>` ブランチを push します。あなたのチェックアウト、インデックス、ブランチには手を触れません。
Codex cloud は、push 済みブランチ上の少量の変更を開始時の diff として受け取ることもでき（`--starting-diff`）、Claude Code は
GitHub にないリポジトリを独自のアップロードとして受け取れます（`--bundle`。この場合クラウドは作業を push し返せません）。
Jules、Devin、Amp には開始ブランチを指定できないため、ブリーフィングでクラウドのエージェントにまずそのブランチを
チェックアウトするよう依頼します。

### 戻ってくるもの

| クラウド | 状態 | 経由 | 会話の戻り方 | コードの戻り方 |
|---|---|---|---|---|
| Claude Code cloud | 対応済み | `claude --teleport`（ターミナルで） | 丸ごと、Claude Code 自身のコピーとして | その `claude/…` ブランチ。このマシンでは `hopsesh/from/claude-cloud/…` として保持 |
| Codex cloud（旧タスク） | 対応済み | `codex cloud status`、`codex cloud diff` | タスクのタイトルと結果を、セッションとして記録 | その diff を `hopsesh/from/codex-cloud/<id>` にコミット |
| GitHub Copilot cloud agent | 実験的 | `gh agent-task view --log` | セッションログをテキストで | その pull request のブランチ |
| Amp | 実験的 | `amp threads markdown` | スレッドをテキストで | orb に残る |
| Jules | 実験的 | `jules remote pull` | Jules に残る | そのパッチを `hopsesh/from/jules/<id>` にコミット |
| Devin | 実験的 | `devin list` | Devin に残る | その pull request のブランチ |

**実験的**とは未検証という意味です。hopsesh はこの 4 つをベンダーのコマンドラインで操作しますが、その出力の大半は
ドキュメント化されておらず、テストも実際のサービスではなくスタンドイン（代役）に対してのみ行っています。粗削りな部分が
あると思われますので、見つけたことをぜひ報告してください。

コードは常にチェックアウトの隣の新しいワークツリーに入り、チェックアウト自体はそのまま残ります。テキストは、引き渡し元の
エージェントの新しいセッションとして書き込まれます（それ以外は Claude Code。`--in codex` で Codex を選択）。`--append` を
使い、引き渡し元のセッションが離れたときのままであれば、そのセッションに追加されます。対応しているのは古い Codex cloud
環境のみで、codex コマンドは現在の Codex cloud（chatgpt.com）で作られた環境をまだ使えません。Codex cloud はすべてのタスクを
Web 上で作成した**環境**で実行します。`--env` で指定してください。プランには最近のタスクで使った環境が候補として表示され、
hopsesh はリポジトリごとに選択を記憶します（`hopsesh clouds env codex-cloud`）。

### ターミナルで行う Claude Code の手順

Claude Code は、あなたが応答できるターミナルでしかクラウドセッションを開始したりコピーを持ち帰ったりしないため、どちらの
手順も目に見えるターミナルで実行されます：アプリの hopsesh Terminal のタブ（Settings → Terminal で選んだ場合はいつもの
ターミナルアプリ）、またはターミナル UI 自身のターミナルです。初回には Claude Code が hopsesh の引き渡し用フォルダーを
信頼するか尋ねます。リポジトリごとに一度あなたが答えるもので、hopsesh が代わりに答えたり、それを省くために Claude Code の
設定を変えたりすることはありません。**Claude Code は、あなたがコピー内でメッセージを送るまでそのコピーを保存しません**。
1 つ送れば（「ok」だけでも）hopsesh がコピーを取り込みます。詳細は[英語版](README.md#claude-codes-steps-in-your-terminal)を
参照してください。

### クラウドからクラウドへ

クラウドセッションは別ベンダーのクラウドへ渡すこともできます。クラウド同士で直接渡す手段はないため、hopsesh はまず
このマシンに持ち帰り（コピーはこのマシンに残り、Claude Code の場合は丸ごと）、そのコピーを引き渡します：
`hopsesh handoff claude-cloud:<id> --to codex-cloud` は `claude/…` ブランチをそのまま使って Codex cloud のタスクを開始し、
`hopsesh handoff codex-cloud:<id> --to claude-cloud` はタスクの diff をこのマシンでコミットし、Claude Code 用の引き渡し
ブランチに push します。プランには両方の行程とその過程で失われるものが表示され、コピーの系譜には両方が記録され、
1 回の undo で両方を元に戻せます。

### undo とブランチの後片付け

`hopsesh undo` は、引き渡しがこのマシンで行ったことを元に戻します：引き渡しブランチ（クラウドがまだ push しておらず、
残すことを選んでいない場合のみ）と、引き渡しの記録です。クラウドセッション自体はベンダーの一覧に残るので、そこで
アーカイブしてください。アーカイブできるベンダーのコマンドラインはありません。持ち帰りを undo すると、それが作った
ワークツリー、コピー、ブランチが削除されます。引き渡した作業がデフォルトブランチにマージされたら、
`hopsesh clouds cleanup`（アプリでは **Activity → Look for merged branches**）が引き渡しブランチとクラウド自身の
`claude/…` や `copilot/…` ブランチの削除を提案します。リモートには読み取り専用で問い合わせ、あなたが確認したものだけを
削除します。`[clouds.<name>]` の下に `delete_branch = "never"` または `"on-undo"` を設定すると、これらのブランチは残ります。

### プライバシー

- クラウドが受け取るのはブリーフィングとブランチだけで、それ以外は何もありません。認証情報らしきファイル（`.env*`、
  `*.tfvars`、`id_rsa*` などの SSH 鍵、`*.pem`、`*.key`、`*.p12`、`.npmrc`）、Git LFS のファイル、50 MB を超えるファイルは、
  何を選んでも送られません。未追跡のファイルは名前を指定した場合にのみ送られます（`--untracked docs/plan.md`）。
- 会話そのものがブランチに載るのは、あなたが求めた場合だけです（`--history-file`。`.hopsesh/handoff.md` をコミット）。
  そのブランチを見られる人なら誰でも読めます。
- 秘密情報のマスクはベストエフォートであり、保証ではありません。何をマスクしたかはプランに表示されます。
- クラウドセッションが保持する内容は、そのベンダーの規約に従って扱われます。これらのコマンドの出力の多くはドキュメント化
  されていないため、hopsesh は慎重に読み取ります。すべてのツールのスタンドインに対してテストしており、Claude Code cloud と
  Codex cloud の一部は実際のツールでも手動で確認しています。実験的なクラウドはまだです。

<a id="use-it-from-your-agent"></a>

## エージェントから使う

```sh
hopsesh skill install              # your agent asks before each hopsesh command
hopsesh skill install --add-rules  # read-only hopsesh commands run without asking
```

エージェントの中からプラグインとしてスキルを追加することもできます（上記の `hopsesh` コマンドは引き続き必要です。
`hopsesh skill install` を使えばプラグインは不要です）。

```sh
/plugin marketplace add roeehrl/hopsesh      # Claude Code, then /plugin install hopsesh@hopsesh
codex plugin marketplace add roeehrl/hopsesh # Codex
npx skills add roeehrl/hopsesh               # any agent that reads Agent Skills
```

スキルは、インストールされているすべてのエージェント（Claude Code と Codex）に同一のファイルとして入ります。あとは
「ノートパソコンのセッションをここに持ってきて」「これを Codex で続けて」「studio で動いているセッションは？」のように
頼むだけです。エージェントはまずプランを立て、**あなたが承認してから移動します**。`--add-rules` を使うと読み取り専用の
コマンド（`ls`、`show`、`plan`、`agents`、`clouds`、`doctor` など）は確認なしで実行されますが、`pull`、`push`、`handoff`、
`followup`、`undo` は auto モードでも**必ず確認します**。「これを Claude Code cloud に引き渡して」（Codex cloud、Copilot、
Jules、Devin、Amp も同様）も同じ流れです：エージェントはプランと残るものを示し、Codex cloud に必要な環境を尋ね、承認後に
引き渡します（Claude Code cloud の場合は、あなた自身のターミナルで実行するコマンドを示し、そこで Claude Code がセッションを
開始します）。スキルが別のエージェントを起動したり、パスワードを扱ったりすることはありません。パスワードでログインする
マシンについては、`hopsesh hosts setup-key` を自分で実行するようエージェントが依頼します。`hopsesh skill remove` で
取り除けます。詳細：[docs/design.md §13](docs/design.md#13-the-hopsesh-skill-for-every-agent)、
[§14](docs/design.md#14-the-command-line-tool-from-the-app)。

## 仕組み

hopsesh は `tailscale status --json` と `~/.ssh/config` からマシンを見つけ、システムの `ssh` で接続してセッションを
一覧表示します。移動時にはまずプランを作り、セッションのファイルをステージング用フォルダーにコピーし、パスを一度に書き換え
（id、署名、暗号化された内容には触れません）、検証してからエージェントが探す場所に配置し、undo 用のジャーナルを残します。
別のエージェントで続けるときは、セッションを中立的な形式に読み込んでから相手のエージェントの形式で書き出します。詳しくは
英語版の [How it works](README.md#how-it-works) と [docs/design.md](docs/design.md) を参照してください。

### データの扱い

- **自分のマシンだけ。** セッションはあなた自身の SSH で移動します。hopsesh のサーバーも中継もテレメトリーもありません。
  ベンダーのクラウドにセッションが送られるのは、あなたが引き渡したときだけです。アプリは、あなたが許可した場合に限り、
  1 日 1 回 GitHub で新しいバージョンを確認します。
- **あなたが「実行」と言うまでは読み取り専用。** マシンごとにあなたの許可が必要で、すべての移動はまずプランとして表示されます。
- **認証情報は決して移動しない。** ログイン、鍵、稼働中のソケット、アカウントデータは元の場所に残り、各マシンはそれぞれ
  サインインしたままです。マシンの SSH パスワードは macOS のキーチェーンか Windows 資格情報マネージャーに保存されるか、
  毎回尋ねられ、hopsesh のファイルやコマンドラインに書かれることはありません。
- **トランスクリプトには秘密情報が含まれうる。** hopsesh は移動中に秘密情報らしきものをスキャンし、コピーを伏せ字にできます
  （`--redact`）。すべてのリモート操作はローカルの監査ログに記録されます。
- **モデルと通信するのはあなたのエージェントだけ。** hopsesh が Anthropic や OpenAI の API を呼び出すことはありません。
- **クラウドへはエージェント経由でのみ。** ベンダーのクラウドには、そのエージェント自身のコマンドで、あなたとしてサインインし、
  あなたが許可した後でのみアクセスします。hopsesh がそのログイン情報を読んだりサーバーを呼び出したりすることはありません。
  引き渡しで送られるのはブリーフィング（秘密情報はベストエフォートでマスク）とブランチだけで、認証情報らしきファイルは
  決して送られず、スナップショットのコミットには不透明な id 以外の情報は含まれません。

<a id="why-not"></a>

## なぜ…ではないのか

- **Codex 自身による Claude Code セッションのインポートは？** これは 1 台のマシン上での変換です。hopsesh は求められれば
  それを使い（`--via import`）、複数マシン、リポジトリ、損失レポート、ブリーフィング、そして元のセッションを損なわずに
  戻る道を加えます。
- **全デバイスで見られる Claude Code のセッション一覧は？** 別のデバイスから開いたセッションも、実際には始めたマシン上で、
  そのマシンのファイルを使って動いています。hopsesh はセッションそのものを移すので、あなたがいる場所で、手元のリポジトリの
  コピーを使って続けられ、また戻すこともできます。
- **Codex アプリの SSH ホストへの引き渡しは？** これは Codex のチャットとその Git の状態を、プロジェクトが保存された接続済み
  ホストへ移すものです。hopsesh は Claude Code と Codex の両方について、手持ちのどのマシン間でも、別のエージェントへも、
  クラウドとの間でも、同じことができます。
- **Remote Control は？** Remote Control は、元の場所で動き続けるセッションを操作するためのものです。hopsesh はセッション
  自体を移すので、あなたがいるマシンで、そのマシンのファイルとツールを使って続けられます。移動したセッションで Remote Control
  を有効にすることもできます。
- **`.jsonl` をコピーして `claude --resume` を実行するのは？** うまくいくのは、両方のマシンですべてのパスが同じで、
  プロジェクトフォルダー名が Claude Code の命名規則に合っており、セッションの 2 つ目のコピーがない場合だけです。hopsesh は
  そうしたケースに加え、リポジトリ、ワークツリー、付随ファイル、undo まで扱います。
- **`~/.claude` 全体をマシン間で同期するのは？** 同期ツールは常にすべてをコピーします。hopsesh は選んだ 1 つのセッションを、
  選んだときに移し、新しいマシン向けに整えます。

アプローチの異なる関連プロジェクト：
- [parcels](https://github.com/0xSero/parcels) はリポジトリと稼働中のエージェントセッションを Tailscale 経由でまとめて送ります。
- [go-claude-teleport](https://github.com/mithro/go-claude-teleport) はセッション、その git ワークツリー、tmux ウィンドウを SSH で移します。
- [claude-nomad](https://github.com/funkadelic/claude-nomad) は設定と履歴をパスのリマップ付きで同期します。
- [chronicle](https://github.com/geekmuse/chronicle) は git を通じて履歴を同期します。
- [sessport](https://www.npmjs.com/package/sessport) は 1 台のマシン上でエージェント間のセッション変換を行います。

<a id="faq"></a>

## FAQ

<details>
<summary><b>マシンの外に何か出ていきますか？</b></summary>

クラウドに引き渡したものだけです。セッションは SSH でマシン間を直接移動します。hopsesh にはサーバーもテレメトリーもなく、
Anthropic や OpenAI の API を呼び出すこともありません。引き渡しでは、選んだベンダー自身のコマンドを使い、あなたとして
サインインした状態で、ブリーフィングとブランチをそのクラウドに送ります（[クラウドセッション](#cloud-sessions)を参照）。
</details>

<details>
<summary><b>Tailscale は必要ですか？</b></summary>

いいえ。`ssh` で接続できるマシンならどれでも使えます。`~/.ssh/config` のエイリアス、ProxyJump、ProxyCommand も含みます。
Tailscale はマシンを見つけやすくするだけです。エイリアスの LAN 名が解決できない場合、hopsesh はそのマシンの Tailscale 名に
フォールバックします。
</details>

<details>
<summary><b>SSH 鍵ではなくパスワードでログインするマシンがあります</b></summary>

`hopsesh hosts add <name> <destination> --password` で追加するか、既存のマシンを `hopsesh hosts auth <machine> password` で
切り替えます。アプリでは Machines 画面の「Login」ボタンを使うか、追加時に「This machine logs in with a password」に
チェックを入れます。

hopsesh は接続時にパスワードを尋ねます：
- **macOS と Windows では**、デフォルトでキーチェーンまたは Windows 資格情報マネージャーに記憶します（毎回尋ねるには
  `--keychain=false`）。
- **それ以外では**、ターミナルの非表示プロンプト、またはアプリのダイアログで尋ねます。
- **スクリプトでは**、`--password-stdin` で標準入力から読み取ります。

パスワードが hopsesh のファイルに書かれたり、コマンドラインに置かれたりすることはありません。

さらにおすすめなのが `hopsesh hosts setup-key <machine>`（アプリでは「Set up key login」）です。パスワードで一度ログインして
SSH 鍵を相手側に追加し、鍵でのログインが機能することを確認したうえで、そのマシンを鍵認証に切り替え、パスワードを破棄します。
macOS と Linux のマシンで使えます。`hopsesh hosts` で各マシンのログイン方法を確認できます。詳細：
[docs/design.md §16](docs/design.md#16-password-login)。
</details>

<details>
<summary><b>相手のマシンに hopsesh をインストールする必要はありますか？</b></summary>

いいえ。`pull` に必要なのは相手側の SSH サーバーだけです。相手のマシンの hopsesh が必要なのは、こちらから `push` する
場合だけです（その場合、相手側で `hopsesh receive on` も必要です）。
</details>

<details>
<summary><b>別のエージェントで続けるとき、何が失われますか？</b></summary>

何かを書き込む前に、プランが教えてくれます。通常、会話はテキストとして届き、ツール呼び出しは前のエージェントのアクティビティ
として表示され、長い出力は短縮され、他ベンダーの推理は除外されます。セッションを戻すときは新しい作業だけがオリジナルに
追加されるので、オリジナルの内容は何も失われません。
</details>

<details>
<summary><b>相手のマシンでセッションがまだ動いていたら？</b></summary>

hopsesh はそのままの状態でコピーして引き継ぎます。その後に残されたコピーで送られたメッセージはそちらに残り、オリジナルは
戻すまでブロックされます。hopsesh は、このマシンで開いているセッションを置き換えることを拒否します（`--stop-local` で
先に終了させる場合を除く）。Codex ではこれはターンとターンの間でのみ可能です。Codex にはターンの途中で穏やかに止める方法が
ないため、作業中であれば hopsesh は終わるまで待つよう求めます。一覧では、実行中のセッションは「live working」または
「live idle」と表示されます。
</details>

<details>
<summary><b>セッションを移動した後も古いコピーを使い続けてしまいました。どうなりますか？</b></summary>

再度移動しようとすると、止まって説明します：両方のコピーが変更されており、hopsesh はそれらをマージしません。`--keep-both` で
受け取るコピーを別セッションとして取り込むか、`--replace` でこのマシンのコピーを脇に置いてください（`hopsesh undo` で戻せます）。
</details>

<details>
<summary><b>ユーザー名、ホームフォルダー、OS が違っても大丈夫ですか？</b></summary>

まさにそのためのツールです。パスは新しいマシン向けに書き換えられ、JSON でエスケープされた Windows のパスも対象です。
区切り文字も Windows と macOS/Linux の間で変換されます。マッピングできないパスはそのまま残され、プランに列挙されます。
</details>

<details>
<summary><b>2 台のマシンでアカウントが違う場合は？</b></summary>

hopsesh は各エージェントに、どのアカウントでサインインしているかを尋ねます：Claude Code には `claude auth status`、Codex には
Codex 自身に尋ねます（`auth.json` は決して読みません）。アカウントが異なる場合は、元のアカウントでしか使えないものを除外します：
Claude Code の署名付き thinking ブロック、Codex の暗号化された推理と compaction の記録です。これらは編集されることなく丸ごと
取り除かれ、会話そのものは残ります。
</details>

<details>
<summary><b>macOS が「ローカルネットワーク上のデバイス」について尋ねてくるのはなぜですか？</b></summary>

macOS 15 以降では、アプリが LAN 上のマシンにアクセスする前に許可を求めます。hopsesh アプリはそうしたマシンに初めて接続する
ときに許可を求めて理由を説明し、「許可しない」を選んだ場合は システム設定 → プライバシーとセキュリティ → ローカルネットワーク
へのリンクを示します。Tailscale 経由の接続は影響を受けず、Terminal から実行するコマンドラインツールにはこの許可は不要です。
</details>

<details>
<summary><b>macOS が hopsesh に iTerm2 や Terminal の制御を許可するか尋ねてくるのはなぜですか？</b></summary>

別のアプリでタブを開いたり前面に出したりするには、macOS の「オートメーション」の許可が必要で、アプリごとに一度尋ねられます。
hopsesh はこれを、自身の起動コマンドを実行するタブを開くことと、セッションのタブを選択することにのみ使います。iTerm2 で
「許可しない」を選ぶと hopsesh は代わりに Terminal を開き、Terminal も拒否されるとコピー用のコマンドを示します。あとから
システム設定 → プライバシーとセキュリティ → オートメーション で変更できます。
</details>

<details>
<summary><b>hopsesh は iTerm2 の Python API を使いますか？</b></summary>

あなた自身が有効にしている場合だけです（iTerm2 → Settings → General → Magic → Enable Python API）。hopsesh がそれを有効に
したり、iTerm2 や Claude Code の設定を変えたりすることはありません。有効な場合、引き渡しの手順はいま使っているセッションの
隣の分割ペインで開き、手順のタブを閉じたことをアプリがすぐに把握し、「Show」は実行中のセッションのタブを直接見つけます。
接続ごとに一度 iTerm2 に API cookie を求め（hopsesh に iTerm2 の制御を許可するか macOS が一度尋ねます）、メモリ上にだけ
保持します。hopsesh のクライアントは、セッションに入力すること、出力を注入すること、画面の内容を読むことができません。
そうしたリクエストがそもそも含まれていないからです。API がオフのときは、従来どおり AppleScript を使います。
</details>

<details>
<summary><b>移動を元に戻すには？</b></summary>

`hopsesh undo` は直近の移動または別エージェントでの継続を元に戻し、`hopsesh undo <id>` は特定のものを元に戻します。新しい
ファイルを削除し、置き換えたファイルを復元し、追加したレコードを切り取り、脇に置いたコピーを戻します。他のマシン上でも同様です。
`hopsesh undo --list` で元に戻せるものが表示されます。
</details>

## コントリビュート

コントリビュートを歓迎します。[good first issue](https://github.com/roeehrl/hopsesh/contribute) ラベルの付いた issue から
始めるのがおすすめです。マシンを 2 台用意する必要はありません：`demo/record.sh shell` を実行すると Docker で架空のマシンを
2 台起動し、hopsesh の準備ができた一方に入れます（[demo/README.md](demo/README.md) を参照）。開発環境のセットアップは
[CONTRIBUTING.md](CONTRIBUTING.md)、質問やアイデアは [Discussions](https://github.com/roeehrl/hopsesh/discussions) へ。
セキュリティ上の問題は [SECURITY.md](SECURITY.md) の手順に従って非公開で報告してください。新しいエージェントへの対応を
追加したい場合は、英語版の [Add your agent](README.md#add-your-agent) を参照してください。

## ライセンス

[Apache License 2.0](LICENSE)。「Claude」および「Claude Code」は Anthropic, PBC の商標です。「Codex」および「OpenAI」は
OpenAI の商標です。hopsesh は Anthropic および OpenAI とは提携関係になく、承認や後援も受けていません。
