# navi

navi 是一门语法和 Rust 高度相似、带 GC 的编程语言，AOT 编译为原生二进制，编译速度对标 Go。

- 语言规范：[spec/language.md](spec/language.md)
- 和 Rust std 的差异：[spec/std-diff.md](spec/std-diff.md)
- 实施计划与里程碑：[docs/plan.md](docs/plan.md)，进度：[docs/progress.md](docs/progress.md)

编译器 navi 用 navi 写成（`compiler/`），能编译自身；自研后端和链接器直接生成可执行文件，不需要 C 编译器和系统链接器，任何平台都能生成其余平台的可执行文件。

## 安装

macOS、Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/sunli829/navi-lang/main/install.sh | sh
export PATH="$HOME/.navi/bin:$PATH"     # 写进 ~/.zshrc、~/.bashrc 或 ~/.profile
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/sunli829/navi-lang/main/install.ps1 | iex
```

脚本下载最新 release 里对应平台的包，按 `SHA256SUMS` 校验后装到 `~/.navi`（`%USERPROFILE%\.navi`；`NAVI_HOME` 可以改），里面是 `bin/navi` 和 `lib/std`（navi 在自己旁边找 std，也可以用 `NAVI_STD` 指定）；Windows 上把 `bin` 加进用户的 PATH。**升级**就是再运行一次；装指定版本：`curl -fsSL .../install.sh | sh -s -- v0.0.5`，PowerShell 里 `& ([scriptblock]::Create((irm .../install.ps1))) v0.0.5`。也可以从 [Releases](https://github.com/sunli829/navi-lang/releases) 手动下载解开。

```sh
navi new hello && cd hello    # navi.toml、src/main.navi（同 cargo new）
navi run                      # 构建到 target/hello 并运行
navi test                     # 单元测试、tests/ 里的集成测试和库的文档测试
navi doc --open               # 本 crate、依赖和 std 的 HTML 文档（同 cargo doc）
navi build --target linux-x64 -o hello-linux   # 交叉编译
navi run hello.navi           # 单个文件不需要项目（同 go run）
```

macOS 上用浏览器手动下载的包带隔离标记（脚本安装的没有），第一次运行前执行 `xattr -dr com.apple.quarantine navi-*`（navi 只有 ad-hoc 签名）。

## 从源码构建

用上一个 release 的 navi 引导（同 Go 用旧版 Go 编译自己），不需要 Go 和 C 工具链：

```sh
tools/bootstrap.sh build        # 下载 tools/bootstrap-version 指定的 release，自举到定点
tools/test.sh build/stage2/navi
```

所以 `compiler/` 和 `lib/std` 只能使用那个 release 已经支持的语言功能和 intrinsic：新功能要等发布了支持它的版本、`tools/bootstrap-version` 改到那个版本之后才能在这两处使用。也可以用 `NAVI_BOOTSTRAP=某个 navi` 指定引导用的编译器。

## 目标平台

windows-x64、macos-x64、macos-arm64、linux-x64（glibc）、linux-arm64（glibc）；Android arm64 用 `--target android-arm64`（linux-arm64 的代码，链接到 bionic）。
