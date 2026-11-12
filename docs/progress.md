# 进度记录

计划见 [plan.md](plan.md)。所有工作在分支 `m1-frontend`（worktree `.worktree/m1-frontend`）。

## 已完成

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 | 完成 | spec、std-diff、CLI 骨架、CI |
| M1 | 完成 | parser、名称解析、签名收集、函数体类型检查、穷尽性检查、impl 一致性、`navi check` |
| M2 | 基本完成 | MIR、解释器、std 子集实现、GC shape 实例分析、`navi run` / `navi test`；未做：MIR 优化（常量折叠/DCE/内联）、编译期 const 求值（目前 const 在运行时求值，数组长度用 AST 常量折叠） |
| M3 | 完成（单线程） | codegen 规划（shape 分组、字典槽、vtable）、arm64 编码器（与系统汇编器逐条对拍）、MIR→arm64、Mach-O 目标文件、`cc` 链接、`navi build`、`navi run` 默认走原生；原生运行时 `std::rt::native`（navi 写，经 `extern "C"` 调 libc）、精确浮点格式化 `std::rt::float`；tests/run 全部 golden 测试原生通过（threads 除外，等 M6） |

## 与计划不同的地方（已在 spec / std-diff 中记录）

- 默认类型参数提前到 M1（师傅决定）。
- `dyn`：navi 没有 `Sized`，任何 trait 都能做 `dyn`，不可经 dyn 调用的方法（泛型、按值 self、提到 Self）不进 vtable（spec 1.5）。
- 原始指针字段由 GC 当作可能的堆指针扫描（std 用它实现通过 `&self` 修改的共享结构，相当于 `UnsafeCell`）。
- 基本类型和结构化类型（元组、数组、引用）的 lang trait impl 由编译器内建（Rust 在 core 里用宏写；navi 没有宏）。
- 引用上的算术运算（`&i32 + i32`）由内建 impl 提供（Rust 的 forward_ref impl）。
- `Step` trait（std::iter::Step）由编译器为整数和 char 内建。
- 枚举的 GC 指针图取各变体的并集，GC 需检查候选字是否真的指向堆（非移动 GC 下安全）。
- 关联类型上的约束（`type IntoIter: Iterator<...>`）作为隐含的 where 子句。
- M3 的原生运行时暂时经 `extern "C"` 调 libc（calloc/write/exit/memmove/fmod），没有用计划里的 `.s` 汇编器；系统调用层的抽象和"调 C 前切换系统栈"随 M6 的可增长栈一起做。
- 新增属性 `#[link_name = "..."]`（同 Rust），用于 extern 函数改名。
- shape 分组的键除了各局部变量的 shape，还包含类型实参的 shape（同 Go）：泛型代码可能经指针访问 `S<T>` 的字段，偏移取决于 T，而局部变量看不出来。

## 设计要点（实现时的决定）

- 解释器：Miri 式内存（字节 + 指针重定位）；每帧按具体类型执行；全局解释器锁（GIL），阻塞时释放。
- 运行时接口：`std::rt` 里的 `#[intrinsic]` 函数（alloc、copy、write、panic_raw、spawn/join、chan_*、select_cases、fmt_float、slice/str 原语）。解释器用 Go 实现；原生后端由编译器内联或用 navi 运行时实现。
- 模板字符串：每个 `${}` 生成一个格式化闭包，`fmt::Arguments { pieces: &[Piece] }`，`Piece::Arg(fn(&mut Formatter) -> Result, Spec)`。
- 闭包：被捕获的变量放在堆 cell 里，闭包值 = (代码指针, 环境指针)，环境是 cell 指针的元组。
- GC shape：瘦指针/胖指针/dyn 指针/闭包各为一个 shape，其余类型按自身；每个具体实例化一份字典。

- 原生 ABI（arm64）：x0 = 上下文（函数实例的字典；闭包是环境指针，字典在环境 -8 处），x8 = 返回槽地址，x9 = 调用者 Location（`#[track_caller]`），参数按 8 字节对齐放在调用者栈上；没有寄存器跨调用保存，所有 MIR 局部变量都在栈帧里。
- 字典：每个槽 16 字节 = (代码, 上下文) 或 (vtable, 0)；vtable 每个 dyn 方法一项 (代码, 上下文)。
- 被取地址的局部变量（M3 还没有逃逸分析）一律放进堆 cell；M4 的 GC 和 M6 的逃逸分析会改进。
- defer：每次 `defer` 分配一条记录 [next, code, ctx, argsize, retsize, loc, args...] 挂在帧的链表上，返回时由 `_navi_rundefers` 依次调用；panic 时（M3）不执行 defer，M4 的栈展开补上。
- 算术溢出、除零、移位溢出的检查直接生成在代码里，失败时调 `std::rt::native::panic_raw`；越界调 `panic_bounds`。

## 下一步

M4：分配器 + STW 精确 GC + stack map + panic 展开（执行 defer）。
