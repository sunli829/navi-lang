# navi

navi 是一门语法和 Rust 高度相似、带 GC 的编程语言，AOT 编译为原生二进制，编译速度对标 Go。

- 语言规范：[spec/language.md](spec/language.md)
- 和 Rust std 的差异：[spec/std-diff.md](spec/std-diff.md)
- 实施计划与里程碑：[docs/plan.md](docs/plan.md)，进度：[docs/progress.md](docs/progress.md)

编译器 navic 用 navi 写成（`compiler/`），能编译自身；自研后端和链接器直接生成可执行文件，不需要 C 编译器和系统链接器，任何平台都能生成其余平台的可执行文件。

## 安装

从 [Releases](https://github.com/sunli829/navi-lang/releases) 下载对应平台的包，解开后是 `bin/navic` 和 `lib/std`（navic 在自己旁边找 std，也可以用 `NAVI_STD` 指定）：

```sh
tar xzf navi-v0.1.0-macos-arm64.tar.gz
export PATH=$PWD/navi-v0.1.0-macos-arm64/bin:$PATH
navic build -o hello hello.navi
NAVI_TARGET=linux-x64 navic build -o hello-linux hello.navi   # 交叉编译
```

macOS 上用浏览器下载的包带隔离标记，第一次运行前执行 `xattr -dr com.apple.quarantine navi-*`（navic 只有 ad-hoc 签名）。

## 从源码构建

目前用 stage0（Go 写的原型编译器，已冻结）引导，需要 Go 1.26：

```sh
go build -o navi0 ./cmd/navi
NAVI_STD=$PWD/lib/std ./navi0 build -o s1/navic compiler
NAVI_STD=$PWD/lib/std s1/navic build -o s2/navic compiler
tools/navictest.sh s2/navic
```

## 目标平台

windows-x64、macos-x64、macos-arm64、linux-x64（glibc）、linux-arm64（glibc）；Android arm64 用 `NAVI_TARGET=linux-arm64 NAVI_ELF_LIBC=bionic`。
