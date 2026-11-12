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

## M9（基本完成；未达成与 go build 持平，未做 M:N 和函数粒度增量缓存）

- 自研链接器（`compiler/src/link.navi`）：navic 直接产出 PIE 的 Mach-O arm64 可执行文件，导入函数经 stub + GOT 由 dyld 非惰性绑定，数据里的绝对指针由 dyld rebase（LC_DYLD_INFO_ONLY 的 opcode 格式），带 ad-hoc 代码签名（CodeDirectory，SHA-256 逐页哈希，`compiler/src/sha256.navi`），不再依赖 cc/ld；`NAVI_LINKER=cc` 仍可用系统链接器。自举（stage2 == stage3）和 22 个 golden 测试通过。
- 性能：师傅定的目标是与 go build 持平，性能优先。基准是 `go run ./bench/gen -lines 10000 -out /tmp/bp10`（约 9k 行 navi + 7k 行 std，等价的 Go 程序；两边都输出 `total 173105`），在 M 系列 Mac 上测：

  | | navic | go build |
  |---|---|---|
  | 冷编译（navic 每次都编 std） | 42s → **2.5s** | 0.45s（std 已在缓存里）；清空缓存后 2.1s（含 std，5.4s CPU 并行） |
  | 无改动重编译 | **0.03–0.06s**（构建缓存） | 0.07s |
  | hello world | 0.59s（几乎全是 std 前端） | — |
  | navic 自编译 | 48s → **6.3s** | — |

  做过的事（按收益排）：
  - trait 选择先按 self 类型的最外层构造器过滤 impl（types 阶段 −60%，存活堆 326 → 104 MiB）；
  - 逃逸分析（Steensgaard 指向分析 + 过程间摘要；trait 调用按 self 类型头部只合并可能的 impl；extern C 不保留指针；摘要用依赖工作表求不动点）：装箱减少，HashMap 查找等热路径不再分配；
  - 寄存器提升（callee-saved x19–x28）、基址+偏移寻址、switch 直落；
  - 结构化类型驻留、normalize/deref/方法解析缓存、布局与 shape 缓存；
  - 只为从 main、运行时和语言函数可达的函数体建 MIR、做逃逸分析（其余按需构建，保守装箱）；
  - String 模板直接降级（不再生成闭包和堆单元）；`Vec::extend_from_slice` 对按位可复制的类型直接拷贝（新 intrinsic `is_copy_clone`，stage0 一律回答 false）；`DefaultHasher` 按字处理；
  - GC：分配快路径、除法换乘法、`std::gc::set_min_heap`（navic 从 256 MiB 才开始回收，同 Go 编译器的做法）；
  - 链接：镜像只哈希一次（UUID 由各页哈希导出）、SHA-256 轮函数展开；
  - 构建缓存（`compiler/src/cache.navi`，同 Go 的 action cache）：键 = 编译器 UUID + 选项 + 路径 + 相关环境变量，命中条件是本次读过的每个源文件 SHA-256 不变；`-a` 强制重编，`NAVI_CACHE=off` 关闭。

  完整的对比见 `docs/bench.md`（`tools/bench.sh`）：1 万行冷编译 2.6s 对 0.38s，10 万行 24s 对 2.7s，无改动重编译 0.03s 对 0.10s。10 万行曾经是 50s：`DefaultHasher` 对只在高位不同的键（`id << 32`）产生低位相同的哈希，HashMap 严重聚集，加了终混合后恢复线性。

  还差的：冷编译仍是 go build（std 已缓存）的约 7 倍。剩余时间分布平坦（parse+resolve 0.37s、types 0.57s、mir 0.6s、plan 0.35s、codegen 0.42s、link 0.21s），其中约 0.55s 是每次重编 std 的前端。再往下要靠：std 前端结果的缓存（需要序列化类型和 MIR）、MIR 内联小函数、函数粒度并行（需要 M:N 多线程运行时和线程安全的编译器数据结构）。
  试过但回退：GOGC=200（被 set_min_heap 取代）、早期的小函数内联实验。

- 语言和工具（都只在 navic 里，stage0 冻结；std 里 stage0 编不了的部分用 `#[cfg(feature = "navic")]` 藏起来，含新语法的放在单独的模块文件里，stage0 不会去读）：
  - `panic::catch_unwind` / `resume_unwind` / `PanicPayload`：每个 G 有一串 catch 记录，`try_call` 用 setjmp 式的上下文保存（`_navi_setjmp`/`_navi_longjmp`，存 x19–x28、fp、lr、sp），panic 先打印消息、执行到 catch 帧为止的 defer，再恢复上下文；`thread::spawn` 用它包住线程函数，`join()` 返回 `Err(PanicPayload)`；`tests/run/catch.navi`。
  - `i128` / `u128`：16 字节整数（对齐 8），MIR 降级时把 128 位的算术、比较、移位、取反、数值转换和 switch 改写成 `std::rt::int128` 的调用（两个 u64 半字实现，debug 构建检查溢出、除零）；Display/Debug/十六进制、Hash/Ord 等 shim、常用方法（MIN/MAX、wrapping/checked/saturating、pow、count_ones…）；`Duration::as_nanos` 等改回 `u128`；`tests/run/int128.navi`。字面量最大到 `u64::MAX`。
  - const 泛型：`const N: usize` 形参（项和 impl 上）、实参（字面量、`{ 表达式 }`、别的 const 形参）、从数组长度推断、函数体里 `N` 作值、`[x; N]`；类型上新增 `K_CONST`，长度是形参的数组把长度类型放在 `ret` 里；std 增加 `From<[T; N]> for Vec<T>`；`tests/run/constgen.navi`。不支持含形参的常量表达式（`[T; N + 1]`）。
  - DWARF：debug 构建写 `__DWARF` 段（DWARF 4：编译单元、每个函数一个 subprogram、行号表——行号来自调用、带检查的算术和函数入口），lldb 可以按 `文件:行` 下断点、看源码和带行号的回溯；还没有变量和类型信息。
  - `navic test` / `navic bench`：编译器为 `#[test]` / `#[bench]` 函数合成入口，调用 `std::rt::testing` 的运行器；测试在 `catch_unwind` 里跑，可按名字过滤；基准用 `std::bench::Bencher::iter`（约 0.5 秒自适应迭代），也支持无参的 `#[bench] fn`；`std::hint::black_box`；`tests/harness`。
  - 构建缓存、`std::gc::{collect, stats, set_min_heap}`、`-timings` 显示 GC 统计、`NAVI_STAGE0=1`（让 navic 的 dump 与 stage0 可比，selfcheck 用）、测试标记 `// stage0: skip`。
- 没做的：
  - **M:N 并行调度**。需要：保留 x28 作当前 G 寄存器（寄存器提升少一个）、每个 M 一个 g0 和 pthread、原子指令 intrinsic、调度器和堆的锁、GC 的 STW 协议（把各 G 的栈界限改成毒值，让下一个函数序言进入安全点；可能阻塞的 C 调用前后要有进出协议并把寄存器存进 G）。一千行量级、出错就是内存损坏，这次没有做，保持 M:1。
  - 函数粒度的增量缓存（需要序列化类型和 MIR）；现在只有整个构建的缓存。
  - 冷编译与 go build 持平（见上面的数字）。

## 下一步

1. 冷编译提速：std 前端结果缓存；MIR 层小函数内联；之后才是并行（依赖 M:N 和线程安全的编译器数据结构）。
2. M:N 调度和并发 GC（设计见上）。
3. DWARF 的变量和类型信息；amd64/ELF/COFF 上的自研链接器和 DWARF（现在只有 macOS arm64）。
