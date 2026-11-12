# navi

navi 是一门语法和 Rust 高度相似、带 GC 的编程语言，AOT 编译为原生二进制，编译速度对标 Go。

- 语言规范：[spec/language.md](spec/language.md)
- 和 Rust std 的差异：[spec/std-diff.md](spec/std-diff.md)
- 实施计划与里程碑：[docs/plan.md](docs/plan.md)

当前处于 **M0**：只有规范和 stage0 编译器（Go 实现）的骨架。

## 构建

需要 Go 1.26。

```sh
go build ./cmd/navi
go test ./...
```

## 目标平台

windows-x64、macos-x64、macos-arm64、linux-x64、linux-arm64。
