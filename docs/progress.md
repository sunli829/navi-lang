# 进度记录

计划见 [plan.md](plan.md)。所有工作在分支 `m1-frontend`（worktree `.worktree/m1-frontend`）。

## 已完成

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 | 完成 | spec、std-diff、CLI 骨架、CI |
| M1 | 完成 | parser、名称解析、签名收集、函数体类型检查、穷尽性检查、impl 一致性、`navi check` |
| M2 | 基本完成 | MIR、解释器、std 子集实现、GC shape 实例分析、`navi run` / `navi test`；未做：MIR 优化（常量折叠/DCE/内联）、编译期 const 求值（目前 const 在运行时求值，数组长度用 AST 常量折叠） |
| M7 | 完成（自举所需部分） | 整数/浮点方法（`tools/gennum` 生成 `num.navi`）、精确浮点解析（与 strconv 逐位一致）、`collections::{HashMap, HashSet}`、`hash::DefaultHasher`、`fs`、`path`、`env`、`process::Command`、`io::{Read, Write, BufWriter, Error}`、`str::pattern::Pattern` 及大量 str/String/slice/Vec/Option/Result/Iterator 方法（rev、zip、chain、flat_map、sum、sort_by 等）；OS 功能经 `std::rt` 的 intrinsic，解释器用 Go 实现，原生用 libc；修了两个 stage0 类型检查 bug（发散的 then 分支、绑定类型未规范化）；偏离记录在 spec/std-diff.md 第 5 节 |
| M6 | 完成（M:1） | `std::rt::sched`（navi 写）：绿色线程 G、FIFO 运行队列、`swapcontext` 式切换（arm64/amd64 各一段汇编）、每个 G 独立的懒提交栈（spawn 的 1MB，主线程 64MB）、函数序言检查栈界限（溢出报 `fatal error: stack overflow`）；`thread::spawn/join`、mpmc channel（有界/无界/无缓冲）、`select` 原生实现，算法与解释器一致；死锁检测；GC 扫描所有 G 的栈；golden 测试含 2 万线程、百万元素流水线、跨线程 GC |
| M5 | 完成（Linux/Windows 未在本机实测） | 与架构无关的 lowering（`backend/lower` + `Machine` 接口）、arm64 和 amd64 两套 Machine（编码器都与系统汇编器逐条对拍）、Mach-O/ELF/COFF 写出器、`--target`（macos-arm64、macos-x64、linux-x64、linux-arm64、windows-x64）和 `-c`；macos-x64 在 Rosetta 下通过全部 golden 测试；Linux/Windows 目标文件用 llvm-readobj/objdump 校验了结构、重定位和未定义符号，实际运行交给 CI 矩阵 |
| M4 | 完成 | `std::rt::gc`（navi 写）：按尺寸分级的 span、非移动 mark-sweep、支持内部指针、无指针类型的 noscan span、GOGC=100 式触发、大对象 span 复用；panic 沿帧指针链展开并执行每帧的 defer；MIR 新增 `StorageLive`，被取地址的局部变量在声明处装箱（每次循环迭代是新变量，和 Go 一致，解释器也修正了）；GC 压力测试（约 2GB 分配，峰值 RSS 约 7.5MB） |
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
- M5：Linux 也经 libc（`cc` 链接）而不是直接 syscall；交叉编译只生成目标文件（`navi build --target T -c`），链接需要目标平台的工具链，M9 的自研链接器解决。Windows 运行时用 `_write`、`VirtualAlloc`（先保留地址空间再按 span 提交），大于 4KB 的栈帧逐页探测。外部 C 函数的地址作为值（`extern "C" fn` 指针）在 Linux PIE 下需要 GOT 重定位，暂不支持。
- 新增属性 `#[link_name = "..."]`（同 Rust），用于 extern 函数改名。
- **M6 是 M:1 调度**（计划是 G-M-P 多 OS 线程并行 + 并发 GC + 写屏障）：所有 G 在一个 OS 线程上协作调度，语义和解释器的 GIL 一致，没有并行；栈是固定大小的懒提交虚拟内存，不做复制增长，所以每个活着的 G 至少占一页物理内存（macOS arm64 一页 16KB），百万级线程做不到。并行调度（M:N）、可增长小栈（栈复制）和并发 GC 都需要更多的运行时机制（原子操作、线程本地 G、STW 协调、写屏障），留到 M9 之后，等自举完成再做。
- **GC 扫描是保守的**（计划写的是精确 GC + stack map）：栈、static 和需要扫描的堆对象都逐字检查"是否指向已分配对象（含内部）"；不含指针的类型（字符串缓冲、数值数组）放在 noscan span，不扫描。这样做可行，是因为生成的代码不在寄存器里跨调用保存值，且被取地址的局部变量都在堆上，栈上没有指向栈的指针（M6 移动栈时只需修正帧链和返回槽这两种已知位置）。代价是整数恰好像堆地址时会误保留对象。精确的类型位图留到 M9 性能阶段。
- shape 分组的键除了各局部变量的 shape，还包含类型实参的 shape（同 Go）：泛型代码可能经指针访问 `S<T>` 的字段，偏移取决于 T，而局部变量看不出来。

## 设计要点（实现时的决定）

- 解释器：Miri 式内存（字节 + 指针重定位）；每帧按具体类型执行；全局解释器锁（GIL），阻塞时释放。
- 运行时接口：`std::rt` 里的 `#[intrinsic]` 函数（alloc、copy、write、panic_raw、spawn/join、chan_*、select_cases、fmt_float、slice/str 原语）。解释器用 Go 实现；原生后端由编译器内联或用 navi 运行时实现。
- 模板字符串：每个 `${}` 生成一个格式化闭包，`fmt::Arguments { pieces: &[Piece] }`，`Piece::Arg(fn(&mut Formatter) -> Result, Spec)`。
- 闭包：被捕获的变量放在堆 cell 里，闭包值 = (代码指针, 环境指针)，环境是 cell 指针的元组。
- GC shape：瘦指针/胖指针/dyn 指针/闭包各为一个 shape，其余类型按自身；每个具体实例化一份字典。

- 原生 ABI（arm64）：x0 = 上下文（函数实例的字典；闭包是环境指针，字典在环境 -8 处），x8 = 返回槽地址，x9 = 调用者 Location（`#[track_caller]`），参数按 8 字节对齐放在调用者栈上；没有寄存器跨调用保存，所有 MIR 局部变量都在栈帧里。
- 字典：每个槽 16 字节 = (代码, 上下文) 或 (vtable, 0)；vtable 每个 dyn 方法一项 (代码, 上下文)。
- 被取地址的局部变量（还没有逃逸分析）在 MIR 里一律装箱（`StorageLive` 处分配新 cell）；以后加逃逸分析减少堆分配。
- defer：每次 `defer` 分配一条记录 [next, code, ctx, argsize, retsize, loc, args...] 挂在帧的链表上（每个帧 x29-32 处是链表头），返回时由 `_navi_rundefers` 依次调用；panic 时 `std::rt::native::unwind` 沿帧指针链到 main 的帧为止，逐帧执行 defer，然后打印 panic 信息并以 101 退出（与解释器、Go 的顺序相同）。
- 算术溢出、除零、移位溢出的检查直接生成在代码里，失败时调 `std::rt::native::panic_raw`；越界调 `panic_bounds`。

## M8 自举（完成）

用 navi 重写的编译器在 `compiler/`（navic），按 stage0 的包逐个移植，每移植完一层就用调试输出和 stage0 逐字节对比（`tools/selfcheck.sh`，覆盖 tests/ 下全部 47 个 crate 和编译器自身）：

| 阶段 | 状态 | 对比方式 |
|---|---|---|
| 词法、语法 | 完成 | `navic parse`（AST 的 S 表达式）与 stage0 一致 |
| 名称解析 | 完成 | `resolve-dump`（每个路径、绑定的解析结果） |
| 类型检查 | 完成 | `types-dump`（所有签名，及每个函数体里每个表达式、局部变量、模式的类型、被调对象、隐式转换），错误信息逐条一致 |
| MIR 构建 | 完成 | `mir-dump`（每个函数体和闭包的 MIR 打印） |
| 布局、shim、代码生成规划、lowering、arm64、Mach-O、驱动 | 完成 | `build -c` 的目标文件与 stage0 逐字节相同（全部 22 个运行测试和编译器自身） |
| 自举 | 完成 | `tools/bootstrap.sh`：stage0 → stage1 → stage2 → stage3，stage1 编译自身得到的目标文件与 stage0 相同，stage2 与 stage3 逐字节相同；stage2 编译的 22 个 golden 测试全部通过 |

navic 目前只生成 macos-arm64（Mach-O）；amd64、ELF、COFF 仍由 stage0 提供，按需再移植。navic 编译自身约 48 秒（stage0 约 0.6 秒），生成代码没有寄存器分配、没有内联、所有被取地址的局部变量都装箱，这是 M9 性能工作的起点。

移植中的做法：
- Go 里以指针为键的 map 改成以 id 为键：AST 节点带全局唯一 id，`Def` 带下标 id，类型带驻留 id；为 `impl Trait` 参数合成的类型参数从 0x40000000 起编号。
- Go 里遍历 map 会产生输出差异的地方，stage0 改成按记录顺序遍历（不改变行为，只让结果确定）：类型检查的结果记录、MIR 按定义顺序构建（闭包编号因此固定）、装箱时被取地址的局部变量按编号处理。
- 发现并修正的 stage0 bug：发散的 then 分支、绑定类型未规范化、or 模式的绑定（每个分支一个新局部变量）、`&mut T: Clone`、const 每次使用都求值、静态初始化顺序。

## M9（进行中）

- 自研链接器（`compiler/src/link.navi`）：navic 直接产出 PIE 的 Mach-O arm64 可执行文件，导入函数经 stub + GOT 由 dyld 非惰性绑定，数据里的绝对指针由 dyld rebase（LC_DYLD_INFO_ONLY 的 opcode 格式），带 ad-hoc 代码签名（CodeDirectory，SHA-256 逐页哈希，`compiler/src/sha256.navi`），不再依赖 cc/ld；`NAVI_LINKER=cc` 仍可用系统链接器。自举（stage2 == stage3）和 22 个 golden 测试通过。
- 性能：师傅定的目标是与 go build 持平，性能优先，其余 M9 特性延后。已做：GC 热路径手工内联、分级查表（自编译 48s → 33s）、lowering 的基址+偏移寻址和 switch 直落（→ 29.5s）。试过但回退：GOGC=200（快 15%，峰值内存 1.1GB → 1.5GB）、小函数内联（前端几乎无收益）。

## 下一步

M9：性能（自研链接器、增量缓存、并行；生成代码的寄存器分配、内联、逃逸分析）和自举后的语言特性。按计划，从 M8 起新功能只加到 navic。
