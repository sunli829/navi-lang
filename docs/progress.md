# 进度记录

计划见 [plan.md](plan.md)。所有工作在分支 `m1-frontend`（worktree `.worktree/m1-frontend`）。

## 已完成

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 | 完成 | spec、std-diff、CLI 骨架、CI |
| M1 | 完成 | parser、名称解析、签名收集、函数体类型检查、穷尽性检查、impl 一致性、`navi check` |
| M2 | 基本完成 | MIR、解释器、std 子集实现、GC shape 实例分析、`navi run` / `navi test`；未做：MIR 优化（常量折叠/DCE/内联）、编译期 const 求值（目前 const 在运行时求值，数组长度用 AST 常量折叠） |
| M7 | 完成（自举所需部分） | 整数/浮点方法（`tools/gennum` 生成 `num.navi`）、精确浮点解析（与 strconv 逐位一致）、`collections::{HashMap, HashSet}`、`hash::DefaultHasher`、`fs`、`path`、`env`、`process::Command`、`io::{Read, Write, BufWriter, Error}`、`str::pattern::Pattern` 及大量 str/String/slice/Vec/Option/Result/Iterator 方法（rev、zip、chain、flat_map、sum、sort_by 等）；OS 功能经 `std::rt` 的 intrinsic，解释器用 Go 实现，原生用 libc；修了两个 stage0 类型检查 bug（发散的 then 分支、绑定类型未规范化）；偏离记录在 spec/std-diff.md 第 5 节 |
| M6 | 完成（stage0 构建 M:1；navic 构建 M9 起 M:N） | `std::rt::sched`（navi 写）：绿色线程 G、FIFO 运行队列、`swapcontext` 式切换（arm64/amd64 各一段汇编）、每个 G 独立的懒提交栈（spawn 的 1MB，主线程 64MB）、函数序言检查栈界限（溢出报 `fatal error: stack overflow`）；`thread::spawn/join`、mpmc channel（有界/无界/无缓冲）、`select` 原生实现，算法与解释器一致；死锁检测；GC 扫描所有 G 的栈；golden 测试含 2 万线程、百万元素流水线、跨线程 GC |
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
- **M6 是 M:1 调度**（计划是 G-M-P 多 OS 线程并行 + 并发 GC + 写屏障）：所有 G 在一个 OS 线程上协作调度，语义和解释器的 GIL 一致，没有并行。M9 起 navic 构建的程序是 M:N（见 M9 一节），stage0 构建的仍是 M:1；GC 仍是 stop-the-world，不是并发的；栈是固定大小的懒提交虚拟内存，不做复制增长，所以每个活着的 G 至少占一页物理内存（macOS arm64 一页 16KB），百万级线程做不到。并行调度（M:N）、可增长小栈（栈复制）和并发 GC 都需要更多的运行时机制（原子操作、线程本地 G、STW 协调、写屏障），留到 M9 之后，等自举完成再做。
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
| 自举 | 完成 | `tools/bootstrap.sh`：上一个 release 的 navic（`tools/bootstrap-version`，现为 v0.0.1）→ stage1 → stage2 → stage3，stage2 与 stage3 逐字节相同（原来从 stage0 引导；两条路得到的 stage2 逐字节相同） |

（当时）navic 只生成 macos-arm64（Mach-O）；amd64、ELF、COFF 后来在 M9 移植过来了（见 M9 的“其他平台”）。navic 编译自身约 48 秒（stage0 约 0.6 秒），生成代码没有寄存器分配、没有内联、所有被取地址的局部变量都装箱，这是 M9 性能工作的起点。

移植中的做法：
- Go 里以指针为键的 map 改成以 id 为键：AST 节点带全局唯一 id，`Def` 带下标 id，类型带驻留 id；为 `impl Trait` 参数合成的类型参数从 0x40000000 起编号。
- Go 里遍历 map 会产生输出差异的地方，stage0 改成按记录顺序遍历（不改变行为，只让结果确定）：类型检查的结果记录、MIR 按定义顺序构建（闭包编号因此固定）、装箱时被取地址的局部变量按编号处理。
- 发现并修正的 stage0 bug：发散的 then 分支、绑定类型未规范化、or 模式的绑定（每个分支一个新局部变量）、`&mut T: Clone`、const 每次使用都求值、静态初始化顺序。

## M9（基本完成；未达成与 go build 持平，未做函数粒度增量缓存）

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

  编译器内部并行（M:N 运行时之后；navic 自己编出来的 navic 才是多线程的，stage0 编出来的 stage1 仍是 M:1）。输出与线程数无关：`NAVI_MAXPROCS` 为 1、3、8、16 时编出的编译器和 10 万行项目逐字节相同，这是每次改动都要做的检查。
  - 解析：按 `mod` 声明一层一层发现文件，同一层的文件在多个线程上解析；解析器有自己的 `IdGen`，每个文件按发现顺序和文件大小分到一段 id（`NAVI_STAGE0=1` 时保持串行编号，selfcheck 用）。
  - 类型检查：函数体分给多个线程，每个线程一份 `Collector` 浅拷贝（错误、BodyInfo、隐藏类型各自收集，结束后按定义顺序合并；常量求值加锁；inherent 表事先建好）。`Ctx` 的驻留表分成 64 片，并行时每片一把锁，类型 id 改成"分片 + 65 × 序号"（id 只用作身份，不影响输出顺序）。
  - MIR：从根出发一层一层构建可达函数，同一层在多个线程上构建（每个函数一份 `Program` 浅拷贝，闭包在函数内编号）；每层先并行检查这一层里还没检查的 std 函数体；`Info` 的惰性查询（body、hidden_of、normalize、deref_target）在并行时持一把可重入锁（用新加的 `std::thread::current().id()`）；MIR 和 AST 的 id 改用新加的 `std::sync::atomic::AtomicUsize`。逃逸分析的摘要不动点保持串行（Gauss-Seidel 式，结果依赖顺序），其后的 keep 和装箱并行（`util::par_for`）。
  - trait 求解：`Info` 的查询（normalize、resolve_method、deref_target）只在读写缓存时持锁，求解本身在锁外、用 Collector 的一份副本做（两个线程可能算同一个查询，结果相同）。
  - plan：实例按层遍历（运行时的实例先走完，它们的静态变量先初始化——生成代码经运行时分配内存，实例图里看不到这条依赖）；每层先在多个线程上做一遍只读的遍历，把具体类型、方法解析的缓存填好、算出每个实例的形状键，然后按顺序真正遍历和分组。没有类型参数的实例直接用函数 id 作组键。
  - 逃逸分析按调用图自底向上（同 Go）：先并行地找出每个函数用到哪些函数的摘要，再按强连通分量从被调用者往上算，单个函数算一次、互相递归的一组迭代到不动点，同一深度的分量并行。
  - 单线程热点（剖析驱动）：类型上的判断（有无推断变量、投影、Self、occurs）不再用闭包遍历（每次都要分配）；整数、bool、char 的 Hash 调用 `Hasher::write_u64`（新加 Rust 的 `write_u8`…`write_u64`），DefaultHasher 一次混一个字；HashMap 扩容直接用清零的内存当空表（`Vec::set_len`）；汇编器缓冲跨函数复用；trait 选择先做不实例化的结构预筛，具体查询（无变量、参数、投影）的选择结果跨函数、跨线程缓存（求解中报过错的不缓存）；查询缓存分 16 片各自加锁。
  - 锁争用：链接改用符号编号，不再按名字查表；后端合并时每个函数的字符串和位置只编号一次；GC 延迟清扫（同 Go），分配时持锁最多扫 256 个 span，其余由清扫 G 做；写屏障按 M 缓冲标记的对象，满 256 个才共享。
  - 后端：工作线程先取最大的函数（同 Go），每次 4 个；类型驻留、布局和类型检查器的缓存在锁后面，布局、字段类型按线程缓存，不含类型参数的类型不拿锁；字符串和位置先用占位名（`%S<i>`/`%L<i>`），按函数顺序合并时才起正式名字，闭包按组内编号。
  - 链接：代码签名的页哈希多线程计算。
  - 顺带发现并修掉的编译器 bug：闭包捕获 `self` 时，`self` 的堆单元会占掉下一个参数的局部变量槽（stage0 和 navic 都有；stage0 冻结，所以编译器源码里不让闭包捕获 self）。`tests/run/closure_self.navi`。
  - 结果（8 核）：bp10 1.75s → 0.93s（go build 0.37s）；10 万行 20.8s（STW GC）→ 8.4s（go build 2.7s），其中 parse+resolve 1.0s → 0.73s、types 3.9s → 1.3s、mir 3.8s → 2.1s、plan 2.9s → 1.6s、codegen 3.2s → 1.8s、link 1.8s → 0.9s；自编译 4.9s → 3.0s。见 `docs/bench.md` 第三轮。

- 语言和工具（都只在 navic 里，stage0 冻结；std 里 stage0 编不了的部分用 `#[cfg(feature = "navic")]` 藏起来，含新语法的放在单独的模块文件里，stage0 不会去读）：
  - `panic::catch_unwind` / `resume_unwind` / `PanicPayload`：每个 G 有一串 catch 记录，`try_call` 用 setjmp 式的上下文保存（`_navi_setjmp`/`_navi_longjmp`，存 x19–x28、fp、lr、sp），panic 先打印消息、执行到 catch 帧为止的 defer，再恢复上下文；`thread::spawn` 用它包住线程函数，`join()` 返回 `Err(PanicPayload)`；`tests/run/catch.navi`。
  - `i128` / `u128`：16 字节整数（对齐 8），MIR 降级时把 128 位的算术、比较、移位、取反、数值转换和 switch 改写成 `std::rt::int128` 的调用（两个 u64 半字实现，debug 构建检查溢出、除零）；Display/Debug/十六进制、Hash/Ord 等 shim、常用方法（MIN/MAX、wrapping/checked/saturating、pow、count_ones…）；`Duration::as_nanos` 等改回 `u128`；`tests/run/int128.navi`。字面量最大到 `u64::MAX`。
  - const 泛型：`const N: usize` 形参（项和 impl 上）、实参（字面量、`{ 表达式 }`、别的 const 形参）、从数组长度推断、函数体里 `N` 作值、`[x; N]`；类型上新增 `K_CONST`，长度是形参的数组把长度类型放在 `ret` 里；std 增加 `From<[T; N]> for Vec<T>`；`tests/run/constgen.navi`。不支持含形参的常量表达式（`[T; N + 1]`）。
  - DWARF：debug 构建写 `__DWARF` 段（DWARF 4：编译单元、每个函数一个 subprogram、行号表——行号来自调用、带检查的算术和函数入口），lldb 可以按 `文件:行` 下断点、看源码和带行号的回溯；还没有变量和类型信息。
  - `navic test` / `navic bench`：编译器为 `#[test]` / `#[bench]` 函数合成入口，调用 `std::rt::testing` 的运行器；测试在 `catch_unwind` 里跑，可按名字过滤；基准用 `std::bench::Bencher::iter`（约 0.5 秒自适应迭代），也支持无参的 `#[bench] fn`；`std::hint::black_box`；`tests/harness`。
  - 构建缓存、`std::gc::{collect, stats, set_min_heap}`、`-timings` 显示 GC 统计、`NAVI_STAGE0=1`（让 navic 的 dump 与 stage0 可比，selfcheck 用）、测试标记 `// stage0: skip`。
- **M:N 调度**（navic 构建的程序；`lib/std/src/rt/sched.navi`）：
  - G 跑在最多每个 CPU 一个的 OS 线程（M）上（`NAVI_MAXPROCS` 可改）；每个 M 在自己的调度记录 g0 上跑调度循环，G 阻塞时把运行时锁交给 g0，g0 取下一个可运行的 G，没有就在条件变量上睡。只有一个 OS 线程时不加锁，所以 navic 自己这类单线程程序几乎没有开销。
  - x28 固定指向当前 G（寄存器提升改用 x19–x27），函数序言比较的栈界限是 G 记录的第一个字；栈检查失败走可恢复的 `_navi_morestack`。
  - 一把运行时锁（pthread mutex）保护调度器和堆；channel 和线程句柄各有一把自旋锁（select 按地址顺序锁住涉及的 channel），G 挂起时由 M 的 g0 在切换之后释放它持有的对象锁，只有唤醒 G 时才拿运行时锁。小对象从每个 M 自己缓存的 span 分配，不加锁（Go 的 mcache 思路），span 用完才在锁下换。
  - 停住所有 G（STW）：收集者持锁，把正在运行的 G 的栈界限改成毒值，G 在下一个函数序言进入 morestack 停下；阻塞在锁、`write`、`system`、文件读写里的 G 在进入前保存了栈指针和寄存器，收集者用 CAS 认领。
  - 并发 GC（Dijkstra 插入写屏障 + 黑色分配）：一个周期先短暂 STW 标记根（栈、寄存器、静态区）并打开写屏障，然后一个标记 G 在程序运行的同时排空标记栈；生成的代码在每次往堆里写含指针的值后检查 `_navi_gc_marking`，打开时调 `gc_wb` 标记写入的指针，运行时的内存拷贝（`rt::copy`、channel 的环形缓冲）同样着色；标记期间分配的对象直接是黑的（arena 里周期开始后才有的 span 不扫描）。最后再 STW 一次：重扫栈和静态区（它们没有屏障）、排空、清扫。G 记录的第二个字 `nopreempt` 保证写入和着色之间、标记 G 取出一个对象到扫完之间不会被 STW 打断（否则那个指针会漏标）。如果标记期间程序又分配了同样多，分配的 G 自己把周期做完。`NAVI_GCSTW=1` 退回 stop-the-world，`NAVI_GCVERIFY=1` 在每次并发标记后重扫所有已标记对象，发现漏标就报错。
  - 可增长的栈：spawn 的 G 的栈从 64KB 开始（主线程固定 64MB）。栈检查放在建帧之前，比较的是 `SP - 帧大小`；不够时 `_navi_morestack` 保存全部寄存器、把自己的 SP 和所需字节数记在 G 里，`sched::morestack` 分配至少两倍大的新栈，把已用部分复制过去，把复制出的每个落在旧栈范围内的字（保存的 FP、局部变量的地址、桩保存的寄存器）平移，再平移堆上 catch_unwind 记录里的栈地址；桩按返回的位移量移动 SP 和 FP 后恢复寄存器。旧栈在下一次 morestack 时释放。逃逸分析保证别的堆对象不会指向栈。上限 1GB。
  - 新 intrinsic：`current_g`/`set_current_g`、原子操作（casal/ldaddal/ldar/stlr）、`mstart_address`。
  - 实测：8 个线程的纯计算 2.31s → 0.34s；8 个线程同时大量分配 1.88s → 0.55s（每次都正确，连续 30 次）。并发 GC：100k 行项目的编译 20.8s（STW）→ 16.9s，峰值内存 1.89GB → 2.39GB（周期进行中堆还在长）；8 个线程大量改写老对象里指针的压力测试 3.7s → 2.4s，打开校验连续几十次都没有漏标。写屏障让 bp10 这类小项目慢约 4%（1.75s → 1.82s）。`tests/run/parallel.navi`、`gc_mutate.navi`、`stack_grow.navi`，golden 测试在 `NAVI_MAXPROCS` 为 1/2/3/4/16、`NAVI_GCVERIFY=1`、`NAVI_GCSTW=1` 下都通过。
  - 限制：没有调用的死循环会让 GC 一直等（Go 1.14 之前也这样）；用户自己的 `extern "C"` 阻塞调用没有进出协议；标记只有一个标记 G。
  - 调度器照 Go 的 GMP：每个 M 有自己的运行队列和 runnext 槽（被唤醒的 G 下一个就在唤醒它的 M 上跑），yield 的 G 进全局队列，没事做的 M 从别的 M 偷；空闲的 M 睡在自己的 note（互斥锁 + 条件变量）上，只有没有 M 在找活时才唤醒一个；这些路径都不拿运行时锁（G 停下时切到 M 的调度记录，由它释放 G 的对象锁、把 G 放回队列或回收），唤醒用 CAS 认领 G。channel 来回传的 pairs 1.6s → 0.2s（sys 7s → 0.1s）。
  - std::sync::Mutex 照 Go 的 sync.Mutex：自旋一会儿，再睡在锁自己的等待队列上，解锁时唤醒一个（原来把 G 放回运行队列空转，每圈都拿运行时锁）。
  - 分配器照 Go 的 mcentral/mheap：每个空闲 span 列表一把锁，堆一把锁，都只在 G 不会停下时持有，持锁时间缩到最短（新 span 在锁外初始化，领到的 span 在锁外清扫）；分配只在要收集时拿运行时锁，并在锁内再查一次。
  - channel 的等待分三个 FIFO 队列（等元素的接收者、等空位的发送者、等元素被取走的无缓冲发送者），一个元素只唤醒一个接收者、取走一个元素只唤醒一个发送者，close 唤醒全部；select 等待过之后会把用不上的唤醒转给它的其他 channel。1 万个 G 等同一个无缓冲 channel：17 秒 → 0.45 秒，10 万个 1.65 秒（`tests/run/chan_many.navi`）。
  - 并行标记：一半的线程做并发标记，各有自己的标记栈，多的工作放进共享池，空闲的标记 G 等一会儿没活就退出，最后一个收尾；最后一次 STW 把各标记栈、池和写屏障队列一起排空。多线程大量改写指针的压力测试 2.39s → 1.97s。
  - std：`sync::Mutex`、`sync::atomic::AtomicUsize`、`thread::current().id()`、`thread::available_parallelism`。
  - 顺带修的：macOS 会杀掉被原地覆盖写的已签名可执行文件，navic 现在先删再写输出。
- 泛型补齐（navic）：trait 方法可以返回 `impl Trait`（每个 impl 决定实际类型）；关联常量可以做数组长度（`[u8; T::N]`、`[u8; Self::LEN]`）。顺带修好：对函数返回的 `impl Trait` 值调用 trait 方法在代码生成里从来没对过（opaque 类型现在带着函数的泛型实参，具体化时揭开）。试过但没保留：GC 提前触发标记（Go 式 pacer），周期变多、到目标时要停世界收尾，总时间变差。
- 继续压串行阶段（navic）：
  - 名字解析按条目并行，每个 worker 写自己的表，最后合并；泛型参数的定义按条目顺序编号，局部变量用绑定它的节点编号，所以任何线程数下结果都一样。函数体里声明了条目的，之后再按顺序解析一遍。
  - 内联照 Go 的做法自底向上：按调用图分层，被调函数先内联完，同一层并行。
  - plan：调用目标在线程里就解析好，按序那一遍只登记实例；GC shape 编号化，每个 worker 有自己的缓存。
  - codegen：worker 做完一批函数，主线程立刻放进文本段，不再等全部做完。
  - `std::gc::set_percent`（同 Go 的 `debug.SetGCPercent`），navic 用 200%：编译器的堆里垃圾很少，峰值内存只多 8%。
  - `NAVI_GCTRACE=1` 打出每次收集是怎么结束的；`-timings` 加了每个阶段的 CPU 时间。
  - 修了两个老问题：
    - arm64 上帧超过 4KB 的函数，栈检查从来不触发。`add_imm` 拿检查自己用的 x17 当临时寄存器，结果用 SP 减帧大小算出来的值是错的。以前主 G 的栈有 64MB，所以没暴露。回归测试是 `tests/run/stack_frame.navi`。
    - `HashMap::clone` 按槽位顺序往一个从小长大的表里插，线性探测的链被堆得很长，10 万个符号要 1.3 秒。
  - GC 的扫描循环改成后端生成的叶子例程 `std::rt::scan_words`（arm64），只在碰到指向已分配、未标记对象的字时才回到 navi 代码，标记的 CPU 降了一半。代码签名和构建缓存的 SHA-256 在 macOS 上走 libSystem 的 `CC_SHA256`（用 CPU 的 SHA 指令），链接 0.55s → 0.34s。
  - plan 的线程在起草时就填好实例的 sites 和 targets，并按代码顺序列出会改动 plan 的事（实例、静态量、字符串、vtable）；按序那一遍只回放这个列表，不再走 MIR，产物逐字节相同。
  - 新 span 的对象用 bump 指针分配：不通过对象内存链接，也不清零（从没用过），sweep 时再把剩下的链起来。trait 选择的缓存也收那些变量已经代表具体类型的查询（先 resolve）；缓存的 selection 里内建关联类型也要 resolve，并检查不含变量（之前会把一个推断上下文的变量带进别的上下文）。
  - 修了 scan_words 引入的竞争：例程停下的那个字被并发运行的 mutator 改写了，回到 navi 代码重读后要再检查一遍。`tests/run/goroutines` 偶发的溢出 panic 和段错误就是它，修好后空闲时连跑 40 次全过。
  - 结果（tools/bench.sh，见 docs/bench.md 第五轮）：冷编译 1 万行 0.82s → 0.60s，10 万行 7.16s → 4.34s，是 go build 的约 1.6 倍（第四轮 2.2–2.7 倍）；自编译约 2.5s → 1.7s。
- 寄存器分配（navic 后端）：x19–x27 按活跃区间线性扫描分配（块间活跃性用数据流算，循环里活跃的局部区间自动延长），生命期不重叠的局部共用一个寄存器，不再是"用得最多的 9 个局部各占一个"。它们是 callee-saved，跨调用不用管；GC 照旧保守扫描栈，morestack 和上下文切换会保存寄存器。10 万行 4.0s → 3.0s，navic 小了 4%。tools/bench.sh 第六轮：冷编译是 go build 的 1.1 倍（1 万行）和 1.2 倍（10 万行）。剩下的代码质量问题：比较后先物化成 bool 再判断跳转、到下一条指令的跳转、寄存器之间多余的 mov、序言保存寄存器每个两条指令。
- 窥孔和内联（navic）：
  - 块尾的整数比较只被它的 switch 读时，直接比较后跳转，不再先物化 bool。
  - choose_registers 只遍历活跃集合里置位的位。
  - 运行时的叶子函数（什么都不调用的字读写、preempt_off/on 等）可以内联，运行时函数只内联叶子。
  - 10 万行 3.07s → 2.85s；tools/bench.sh：1 万行 0.41s（Go 0.36s），10 万行 2.98s（Go 2.62s），约 1.14 倍。
  - 试过但没保留（测不出差别）：把不依赖帧的 intrinsic 算作叶子、内联后重新判断运行时函数、内联大小上限 6 → 10、不跨调用的临时值放 x1–x7。
- 减少分配（navic）：按字节抽样的分配剖析（每 256KB 记一次调用栈，用过即删）显示 10 万行一次构建共分配约 2.5GB，分散在很多地方。最大的两处修掉了：choose_registers 每个函数重新分配全部数组（160MB，现在在函数之间复用）、impls_of_trait 每次查询都复制列表（88MB，现在返回共享的只读列表）。峰值内存 2.28GB → 2.08GB。剩下的大头：类型驻留 160MB、emit_out 117MB、小哈希表第一次插入 105MB、重定位里的符号名字符串 78MB。
- 内存剖析和少产生垃圾（navic）：
  - 写了存活剖析（GC 标记完成时看抽样对象还活不活，相当于 Go 的 inuse_space，用过即删）。10 万行在 plan 阶段的活堆约 700MB：MIR 约 245MB、类型（驻留的类型、推断变量、缓存）约 190MB、AST 和 token 约 70MB、plan 约 32MB。活堆里 91% 要逐字扫描，被扫的字只有 26% 是指针。
  - 摘掉 MIR 已建好的函数的类型检查结果，活堆几乎不变（那部分大多是本来就要留的表和缓存），没保留。阈值从 200% 降到 100% 只省 6% 内存、多 20% CPU，也没改。
  - 峰值内存 ≈ 最后一次 GC 时的活堆 + 之后 plan/codegen 产生的垃圾，所以从垃圾下手：
    - 调试行表存文件索引，不再每行复制文件名；
    - 重定位共用符号名，函数的重定位列表直接移交、不再深拷贝；
    - 占位符名按序号只做一次；worker 按文件号和位置找本函数的位置，不再拼 key；panic 点的文件名符号每个文件只找一次；
    - trait 选择缓存对至多一个参数的 trait ref 用数字做 key。
  - 以上产物逐字节不变。总分配 2.47GB → 2.07GB，峰值 2.28GB → 约 1.92GB，user CPU 14.6s → 13.7s。
  - MIR 瘦身：在每个阶段结束时强制收集一次来量活堆（类型检查后 313MB，MIR 后 616MB）。MIR 本身其实不大（25 万条语句、20 万个块），浪费在两处：
    - navi 的 `Vec::new()` 本身要分配，而大多数 place 没有投影、大多数常量没有 bytes——现在共用一个空列表；
    - 块平均只有 1.3 条语句，却按 4 条留了空间（每条语句 112 字节）——内联之后按长度收紧（新增 std `Vec::shrink_to_fit`）。
    MIR 后的活堆 616 → 572MB。剩下的 MIR 增长里，内联拷贝约 56MB，其余是类型替换和惰性检查的 std 函数体；再压表示（Operand 32 → 24 字节之类）只能省一二十 MB，没做。
  - 精确 GC（堆部分）试过，没保留，代码在分支 `precise-heap-wip`。照 Go 做了：每个类型的指针位图描述符（名字即内容，各线程无须协调）、span 的指针位（每字一位）、分配时写位、只扫指针字的叶子例程 `scan_ptrs`。自举、定点、GCVERIFY 都过了，但 user CPU 13.5s → 16.8s：标记只省约 10%（非指针字本来一次比较就跳过，贵的是指针字本身：查 span、查标记位、进出标记栈），分配时写位却贵得多。即使把写位优化到底，最好也只省总 CPU 的约 1.5%。精确 GC 在 Go 里换来的是正确性（不误留对象、栈能精确移动），不是速度。
- 精确栈图（照 Go 的 adjustframe）：栈增长复制时，不再把"看起来像旧栈地址的每个字"都平移，而是按每个函数的栈图来改。
  - 编译器为每个函数生成栈图：
    - 帧内哪些字可能是指针：按局部变量的类型；固定槽、结果缓冲、临时区、传参区按"可能是"处理；
    - 用了哪些 callee-saved 寄存器、各存在哪；
    - 每个调用点的返回偏移，以及 x19–x27 中哪些此刻放着活的指针（由寄存器分配的活跃区间算出）。
  - 还有一张"代码地址 → 栈图"的表 `_navi_ftab`，运行时用 intrinsic `func_table` 读取。
  - 和 Go 不同的地方：提升的局部放在 callee-saved 寄存器里，走帧时要追踪每个寄存器的值存在哪一层（用到这个寄存器的最内层帧决定它是不是指针）。
  - 运行时（std::rt）的函数里常把地址存成整数，所以整帧保守处理；Go 用 `nosplit` 和 uintptr 的约定解决同一个问题。手写桩、没有栈图的代码也保守处理。
  - `NAVI_STACKCONS=1` 退回保守平移；`NAVI_STACKCHECK=1` 列出搬完后仍指向旧栈的字。检查时发现的都是死值：函数入口的临时寄存器，和 Option 标记字里残留的字节——后者正是旧做法会改坏的"像地址的整数"。
  - 栈图的编码参考 Go 的 pclntab：
    - 底部缓冲区只记一个长度，上面的部分用位图；
    - 每个调用点 4 字节（返回偏移和 9 位掩码）；
    - 所有栈图放在一个块里，表项是 32 位偏移，没有逐函数的符号和重定位。
    产物约大 8–12%（编译器 7.66MB → 8.25MB，10 万行 27.0MB → 30.2MB）。
  - GC 扫栈也按栈图来（照 Go 的 scanframe）：
    - 停下的 G 记下仍然有效的帧指针，作为走帧的起点：上下文切换时存的；进入阻塞调用时存调用者的；STW 停在 morestack 里时存 morestack 自己的。
    - 用户代码的帧精确扫：类型是指针的槽，加上调用点掩码标出的寄存器。
    - std 的帧仍保守扫（栈图里的标志 2）。原因是 std 会把堆地址放进整数做运算，那可能是对象唯一的引用；这对应 Go "uintptr 不保活对象"的约定，只要求用户代码遵守。
    - 运行时、手写桩、G 停下时保存的寄存器都保守扫。
    - 所有测试（各线程数、GCVERIFY）、编译器自举（打开 GCVERIFY）、压力测试都通过；速度和内存不变。`NAVI_STACKCONS=1` 同时退回保守搬栈和保守扫栈。
  - 所有测试、自举定点、确定性、压力测试在精确模式下都通过。
  - 两个老问题已修（`NAVI_GCSTW=1` 下 `tests/run/goroutines` 偶发崩溃，4 线程时约七成；`NAVI_GCVERIFY=1` 下 `tests/run/gc` 偶发报"漏标"，约三分之一）。根因是同一个：`release_locks` 在 M 的调度记录 g0 上运行，却用 `Vec::new()` 做了 GC 分配，而收集器既不停 g0 也不扫 g0。现在改用固定数组，超过 64 把锁时用 C 内存。
    - 定位用的是新加的调试模式 `NAVI_GCPOISON=1`：释放的对象填 0xab；发现空闲对象被写过就报错；在 g0 上做 GC 分配也直接报错。
    - 停世界时也不再只等"状态是运行中"的 G：yield 和挂起都是先改状态再切走，期间 G 还在跑，现在凡是还挂在 M 上的 G 都要等。M 回到调度循环时先清 `m.cur` 再做收尾，因为收尾可能要等收集器手里的锁。
    - 修好后两项分别连跑 16 次和 30 次全过。
- match 补齐（对照 Rust）：
  - 切片模式里 `..` 前的绑定（`[a, rest @ ..]`、`[first, mid @ .., last]`）原来会崩，现在绑定成切片；按值的数组绑定也支持。
  - `if let` / `while let` 支持 let 链：`if let Some(x) = a && let Some(y) = b && x < y`。不含 let 的条件照旧生成，MIR 与 stage0 一致。
  - 不可达分支警告：编译器有了警告（`diag::Error.warning`），只有错误才让构建失败；`tests/run` 里用 `// warn: 行:列 消息` 检查。i128/u128 的字面量和区间原来按 0 宽的值域算，会误判；现在当作不透明（不参与覆盖），和字符串一样。
- 解构赋值（同 Rust）：`(a, b) = (b, a)`、`P { x, y: v[i], .. } = p`、`W(a, _) = w`、`[c, .., d] = arr`。原来能编译过，但什么都不赋值。parser 照 Rust 改写成 `{ let ($d0, $d1) = e; a = $d0; b = $d1; }`。顺带修了 `1 = 2` 这类非位置的赋值不报错（现在报 "invalid left-hand side of assignment"）；`tests/run` 加了 `// error: 行:列 消息`，用来检查编译应当失败的用例。
- 其他平台（为删掉 stage0 做准备）：navic 现在支持 stage0 的全部 5 个目标（`--target` 或 `NAVI_TARGET`）。
  - amd64 后端（`backend/amd64.navi`）：
    - 寄存器：G 寄存器放 r14（同 Go），保存寄存器只有 rbx、r12 两个，因为 amd64 只有 16 个通用寄存器。r9、rdx、xmm14、xmm15 是暗中使用的临时寄存器。
    - 运行时例程（morestack、上下文切换、setjmp）的保存槽位与 arm64 相同，所以栈遍历、搬栈和 morestack 协议是两种架构共用的。
    - 栈图里调用的返回偏移按指令单位记录：arm64 是 4 字节，amd64 是 1 字节（`TargetConv.code_unit`，运行时里是 `CODE_UNIT`）。
    - C 调用支持 System V 和 Windows x64 两种约定，包括栈上参数、Windows 的 32 字节影子区，以及 System V 可变参数要求的 al。
    - 有符号除以 -1 不会触发 idiv 的异常：商回绕，余数为 0。
  - macOS x64：自研 Mach-O 链接器也能链接 x86-64（跳板是 `jmp [rip+got]`），不需要系统工具链。
    - 在 Rosetta 下，全部测试在各种模式（线程数、GCVERIFY、GCSTW、GCPOISON）下都通过。
    - x64 版 navic 编译自身能到定点，交叉编译出的 arm64 产物与 arm64 版 navic 编译的逐字节相同。
  - Linux：有自研的 ELF 链接器（`elflink.navi`，同 Go 的内部链接），不需要 C 工具链。
    - 输出是动态加载 libc 的 PIE：导入函数经 GOT 加跳板调用，GOT 在装载时一次绑好（BIND_NOW）；数据里的指针用 RELATIVE 重定位；`_start` 调 `__libc_start_main`；带 DT_HASH，并带 .symtab 和 DWARF 供调试器用。
    - `NAVI_LINKER=cc` 还能改回系统 cc，`-c` 照样生成 ELF 目标文件。
    - arm64 的 ldr 低 12 位用单独的重定位类型（R_AARCH64_LDST64_ABS_LO12_NC）。
    - linux-arm64 的原子操作用 LL/SC 循环，因为 ARMv8.0 的板子（如树莓派 4）没有 LSE。`NAVI_LLSC=1` 可以在 macOS 上试这种写法。
    - 在本机的 Android arm64 模拟器上验证过（Linux 内核加 bionic）。`NAVI_ELF_LIBC=bionic` 会换成 bionic 的动态链接器、libc.so 和 `__libc_init`，这样 navic 链接出的程序可以直接在模拟器上运行。
    - 结果：全部测试在各种模式下都通过；navic 在模拟器上链接自身，产物与 mac 上的逐字节相同。用的脚本是 `tools/androidtest.sh`，加 `ANDROID_LINK=ndk` 则改用 NDK 的 clang 链接。
    - linux-x64 只做了静态检查（llvm-readelf/objdump），没有运行。
  - Windows：有自研的 PE 链接器（`pelink.navi`），不需要 C 工具链。
    - 输出是 PE32+，支持 ASLR（带 .reloc 基址重定位）。Win32 API（首字母大写的函数）从 kernel32.dll 导入，C 库函数从 ucrtbase.dll 导入，都经导入地址表跳板调用。
    - 不带 CRT 启动代码：入口向 ucrtbase 要 argc、argv（`_configure_narrow_argv`、`__p___argc`、`__p___argv`），然后调 main 并 exit。
    - `-c` 生成 COFF 目标文件（`NAVI_LINKER=cc` 时由 clang 链接）。
    - 重定位超过 65535 条时用 IMAGE_SCN_LNK_NRELOC_OVFL。
    - 运行时的线程和锁加了一层抽象：pthread，或 Windows 的 SRWLOCK、CONDITION_VARIABLE、CreateThread、SwitchToThread、GetSystemInfo。
    - navic 在 Windows 上运行要用的也补上了：缓存目录用 LOCALAPPDATA，输出加 `.exe`，PATH 用 `;` 分隔，不调 chmod。
    - COFF 用 NDK 的 lld-link 加上 llvm-dlltool 生成的导入库链接过 hello 和 navic 自身；PE 用 llvm-readobj/objdump 检查过导入表、基址重定位和入口。都没有运行。
- 修了一个偶发死锁（表现为测试卡住，CPU 空转）。
  - 现象：一个 G 拿着运行时锁，却在 `exit_blocked` 里永远等下去，其他线程都在等这把锁。用 lldb 读出这个 G 的 `sys` 是"运行中"。
  - 原因：G 在 `enter_blocked` 之后、真正阻塞之前，又进了一个 navi 函数的序言（比如锁的封装函数）。这时收集器正好在停世界，栈界限是毒化过的，于是进了 morestack。morestack 的停世界分支把 `sys` 改成"已停"、再改成"运行中"，盖掉了"阻塞/已认领"。
  - 修复：morestack 只在 `sys` 为"运行中"时才参与停世界；阻塞窗口里的 G 只等停世界结束，不改状态。`exit_blocked` 遇到"认领"以外的状态时直接报 fatal，不再空等。
- 修了 16 线程编译时约 5% 的崩溃（表现为 panic 或段错误，有时报 grow 里减法溢出）。
  - 定位：关掉并发标记，崩溃率不变；改用保守搬栈（`NAVI_STACKCONS=1`），崩溃消失。新加了调试模式 `NAVI_STACKMOVE=1`：spawn 出来的 G 每次进 morestack 都搬栈，并把栈界限设在当前位置附近，几乎每加深一层调用就搬一次。
  - 在这个模式下，单线程和 4 线程都正常，16 线程每次都崩，说明不是栈图写错了。在 lldb 里看到：morestack 存的 `entry_sp` 落在旧栈里，而 G 实际运行在新栈上；还有一次 `release_os` 把自己正在用的栈释放掉了。
  - 原因：morestack 的桩把栈界限清零，让 morestack 内部调用的函数通过序言检查。但收集器停世界时会把每个运行中的 G 的界限设成最大值，包括正在 morestack 里的 G。于是 grow、move_frames 等函数的序言又进了 morestack。内层的桩覆盖了外层正在用的 `entry_sp` 和 `need`，内层的 morestack 还可能释放旧栈、重复搬栈。
  - 修复：G 的第五个字 `ms_depth` 记录 morestack 的嵌套深度，由桩维护。只有最外层的桩记录 SP 和 need（need 改为放在 I1 里传给桩，函数的溢出路径不再直接写 G）。内层 morestack 直接返回 0；外层结束时如果正在停世界，会重新毒化界限，让 G 在下一个序言处停下。
  - 另外加了两个检查：搬栈时 SP 不在 G 的栈里就报错并打印现场；同一个 G 被两个 M 同时调度也会报错（`running_on`）。
  - 同类的两个问题，在 `NAVI_GCSTW=1 NAVI_STACKMOVE=1` 加 16 线程下暴露出来（约 1/20 的数据损坏）：
    - G 在 `enter_blocked` 之后、真正阻塞之前进了 morestack 并搬了栈。它为收集器保存的 SP/FP 还指着旧栈，收集器扫的是旧栈，漏掉了活对象。而且它搬栈时状态还是"阻塞"，收集器可以同时认领它、扫它的栈。现在这种 G 在 morestack 里先回到"运行中"（被认领就等），搬完把保存的 SP/FP 按搬动距离修正，再回到"阻塞"。
    - 停在 morestack 里的 G，收集器从 morestack 的帧往上扫。桩压的帧记录返回到正在进入的函数里，于是调用者的帧被套上了这个函数的栈图，调用者的指针槽被漏扫。现在 morestack 停下时，把给收集器的 FP 设成 call_morestack 压的那条记录。下面的部分保守地扫，上面从调用者开始按各自的栈图扫（和 move_frames 的处理一致）。
    - 发起停世界的收集器自己进了 morestack（栈要增长，或者压力模式下的界限）时，被当成普通 G 停下，自己等自己。现在记下发起者（`STW_OWNER`），morestack 不会让它停自己。
    - 收集器的栈在停世界期间搬了家：它先取了自己的 SP/FP，再调函数取栈顶，这个调用触发了栈增长，结果扫描范围横跨新旧两个栈，越界读。Go 让收集器在系统栈上运行；navi 改成停世界前先留出 256KB 的栈余量（`stack_headroom`）。停世界期间收集器的栈不再搬（压力模式也不搬），真需要增长就报 fatal，不再悄悄出错。
  - 修完以后，16 线程编译 navic 在各种模式下共 150 次，0 次崩溃（修之前普通模式约 5%，压力模式每次都崩）。
  - `tools/navictest.sh` 给每个测试程序 5 分钟超时，卡住算失败，不会再让整套测试挂住。压力模式只在已用栈不到 256KB 时生效，否则深递归会让拷贝量变成平方级。
- 反射（照 Go 的 reflect，不需要标注；代替过程宏）：
  - 内建函数 `type_desc::<T>()` 返回 T 的描述的地址。规划器为每个到达它的类型，以及这些类型各部分的类型（字段、变体、元素、map 的键和值）生成描述，按类型去重。泛型代码中 T 不同的实例共用代码时，描述放在字典的槽里（`Target.key` 带上类型参数）。
  - 描述有 12 个字，内容是：kind、大小、对齐、名字、元素、长度、字段或变体表、枚举的标记（整数标记或空指针 niche）、标志位，以及 HashMap 的 `ReflectMap` 虚表。
  - Vec、String、HashMap 的空值和类型参数无关，反射可以直接构造。Vec 加元素用 `Vec<u8>` 视图按字节做。只有 HashMap 的插入和遍历要用键的 Hash 和 Eq，所以描述里带它的 `ReflectMap` 虚表，复用了现有的虚表机制。
  - 含指针的值用运行时的拷贝函数复制，带写屏障。
  - 不用反射的程序，产物和以前逐字节相同。
  - `std::json` 和 `std::any::type_name` 都建在反射上。测试是 `tests/run/reflect`、`tests/run/json`。
- 没做的：
  - 函数粒度的增量缓存（需要序列化类型和 MIR）；现在只有整个构建的缓存。
  - 冷编译与 go build 持平（见上面的数字）。

## navic fmt（格式化工具）

- 用法：`navic fmt [--check] [--comment-width N] [路径...]`。路径是 crate 目录时从 `src/main.navi` / `src/lib.navi` 开始，是文件时从这个文件开始，都会顺着 `mod x;` 把子模块一起格式化（同 rustfmt）。不给路径时格式化当前目录的 crate。`--check` 只列出会变的文件和改动，有改动时退出码为 1。`navic fmt -` 从标准输入读、向标准输出写（只支持有 `/dev/stdin` 的系统）。
- 注释折行（师傅定的，默认打开；rustfmt 的 `wrap_comments` 默认关闭）：超过注释宽度的 `//`、`///`、`//!` 注释在空格处折行，折下来的部分接到同一段的下一行（同 rustfmt 的 `wrap_comments`）。代码块、缩进 4 格的内容、标题、表格、引用不动；列表项的续行对齐到文字；`// --- 小节 -----` 这种带一串重复符号的分隔行不折（rustfmt 会折开）；写在代码后面的行尾注释和 `/* */` 不折。注释宽度从行首算，默认 80（同 rustfmt 的 `comment_width`），可以在 crate 的 `navi.toml` 里用 `[fmt]` 表的 `comment_width = N` 设置（从文件所在目录往上找最近的 navi.toml），命令行的 `--comment-width N` 优先。
- 其余风格完全照 rustfmt 的默认配置，2024 风格：最大宽度 100，缩进 4 格，各种宽度阈值相同（参数列表 60、结构体字面量 18、枚举结构体变体 35、链式调用 60、单行 if-else 50）；`use` 和 `mod x;` 按版本序排序，`use` 树会规范化；`loop`/`while`/`for` 后的分号去掉；块末尾的 `return`/`break`/`continue` 补上分号。
- 实现在 `compiler/src/fmt/`，按 rustfmt 的结构移植：Shape（首行剩余宽度和续行缩进）、lists（横排/竖排/混排，行尾注释对齐）、overflow（最后一个参数溢出到后面几行）、chains、closures、`choose_rhs`（赋值右边放同一行还是下一行）、matches、items（函数签名、结构体、枚举、impl、trait、where）、imports。
  - 复用编译器的 lexer 和 parser，编译路径不受影响。注释不在 AST 里：格式化器从 token 之间的空隙取出注释，再按位置放回。列表、块、条目之间、else 前后的注释都会被格式化；其他位置有注释的节点原样保留（rustfmt 也是这样）。
  - 节点只记录了起点，终点由 token 表算出（括号配对、泛型的 `<>`）。
  - 解构赋值被 parser 改写成了 let 块，格式化时识别出来，按原来的写法打印左边。
  - navi 特有的语法：`defer` 按语句处理；`select` 按 match 的规则处理；模板字符串和普通字符串一样原样保留（同 rustfmt 2024，单行字符串不受宽度限制）。
- 安全检查：格式化结果必须能重新解析，并且 AST dump（`syntax::dump`）与原文件一致；比较前两边都抹掉格式化允许的改动（use 和 mod 的顺序、use 树的形状、包住单个表达式的花括号、循环后和 return 处的分号、空语句、重复的括号）。不一致就报内部错误，文件保持不动。
- 验证：
  - 对照 rustfmt 1.11（`--edition 2024`，navic fmt 用 `--comment-width 1000` 关掉注释折行，因为 stable 的 rustfmt 不能打开 `wrap_comments`）：仓库里 rustfmt 能解析的 176 个文件（把单行模板字符串换成等宽的普通字符串），175 个与 rustfmt 的输出逐字节相同。剩下一个是 rustfmt 排序 `use` 时把行尾注释挪到了别的行上，navic fmt 让注释跟着原来那一行。
  - 仓库的全部 `.navi` 文件都能格式化（按默认的注释宽度），通过安全检查，格式化两遍结果不变（只有一个故意写错语法的测试文件不能格式化）。
  - `tests/fmt/` 是格式化的测试用例（`x.navi` 格式化成 `x.out`，`x.out` 保持不变），由 `tools/navictest.sh` 运行，它也检查 `--check` 和 `comment_width` 的配置；`items`、`exprs` 两个用例也与 rustfmt 的输出相同。
- 没做的：行尾注释和块注释的折行；链式调用中间、二元运算中间这些位置有注释时节点原样保留；配置文件（rustfmt.toml）。仓库自己的代码还没有用 navic fmt 格式化过。

## navic lsp（语言服务器）

- `navic lsp`：用标准输入输出说 LSP（JSON-RPC，`Content-Length` 分帧），全部用 navi 写，在编译器前端上实现，代码在 `compiler/src/lsp/`。
- 功能：
  - 诊断：打开、修改、保存时分析文档所在的 crate，发出编译器的错误和警告（范围是整个 token）；std 的不发。
  - 格式化：`textDocument/formatting` 调 `navic fmt`（注释宽度照 crate 的 navi.toml）。
  - 跳转到定义、类型定义、实现（trait 或类型的 impl；trait 方法的各个实现）；查找引用；同一文件里的高亮；重命名（含 prepareRename，只允许改本 crate 里定义的东西，结构体字面量和模式里的字段简写会改成 `x: new`）。
  - 悬停：定义的声明（照源码，到函数体为止）和它上面的 `///` 文档，模块的 `//!` 文档；局部变量和字段显示类型；基本类型。
  - 文档符号（层级：impl 和 trait 的条目、枚举变体、结构体字段）、工作区符号。
  - 补全：`值.` 列出字段和方法（固有 impl、trait impl 和 trait 的默认方法，引用会自动解引用）；`路径::` 列出模块成员、枚举变体、关联函数；否则列出当前函数里此前声明的局部变量、模块里的名字、prelude 和关键字。
  - 函数签名提示：当前实参高亮，方法调用跳过 self。
  - 类型内嵌提示：没写类型的 let、for、if let 绑定。
  - 折叠：括号块、连续的注释、连续的 use。语义高亮：关键字、字符串、数字、注释、标签，以及按解析结果分类的名字（模块、类型、结构体、枚举、trait、类型参数、变量、字段、枚举变体、函数、方法）。
- 实现：
  - 分析就是 `driver::check_specs`（解析、名字解析、类型检查，`cfg(test)` 打开），打开的文档通过 `cache::OVERLAY` 代替磁盘上的文件（`resolve` 读源文件时先查它）。连续的修改合并成一次分析：读消息的线程把消息放进通道，主循环一次取完再分析；请求要用到过期的分析时先重新分析。前端或请求崩溃（panic）时用 `catch_unwind` 接住，只回一个错误。
  - 文档所在的 crate：往上找最近的有 navi.toml、src/main.navi 或 src/lib.navi 的目录；文档不在这个 crate 的模块树里时当作单文件 crate。std 的位置：`$NAVI_STD`，否则往上找 `lib/std`，否则在 navic 旁边找。
  - `lsp/index.navi` 把一个文件里每个名字指向什么（定义、局部变量、字段、基本类型）记下来：路径用 `resolve::Info.paths`（前面的段按父定义的名字对应），方法调用和 `Vec::new` 这类类型相关的路径用类型检查的 callees，字段用接收者的类型，use 里的名字用模块的绑定。std 的函数体按需检查（`Info::body`）。跳转、引用、重命名、悬停、语义高亮、补全都用这个索引。
  - 内存：每份分析都带一整份 std，所以只留最近 4 个 crate 的分析，文档全部关闭的 crate 立即丢掉；GC 节奏改回默认（编译时是 200%）。
  - 速度（M 系列 Mac）：小 crate 一次分析约 70ms，编译器自己（约 4 万行，加 std）约 0.6s。
- 验证：
  - `tests/lsp/session.jsonl` 是录下的一次会话（每行一条消息，仓库路径写成 `@ROOT@`），覆盖上面的每个功能和一次带错误的编辑，`tools/navictest.sh` 把它喂给 `navic lsp` 并和 `tests/lsp/session.out` 比较。
  - 压力测试：仓库的 203 个文件逐个打开，每个文件请求语义高亮、符号、折叠、内嵌提示，并在随机的名字上请求悬停、跳转、补全、签名、高亮、引用，共 13175 次请求，没有错误；抽查的非关键字名字都有悬停结果。
- 编辑器配置（服务器命令都是 `navic lsp`，必要时设 `NAVI_STD`）：
  - Neovim：`vim.lsp.start({ name = "navic", cmd = { "navic", "lsp" }, root_dir = vim.fs.root(0, { "navi.toml", "src" }) })`，文件类型 `navi`。
  - Helix（languages.toml）：`[language-server.navic] command = "navic"` 和 `args = ["lsp"]`，再加一个 `[[language]] name = "navi"`、`file-types = ["navi"]`、`language-servers = ["navic"]`。
  - VS Code 需要一个通用 LSP 客户端扩展（仓库里没有带）。
- 没做的：代码操作（code actions）、调用层级；增量分析（每次都整个 crate 加 std 重新分析）；补全只认得局部变量、字段、方法调用结果这类接收者，泛型参数上的方法不补全。

## 标准库补全（对标 Go 的标准库，API 照 Rust std 和常用 crate）

- Rust std 补齐：集合（VecDeque、BTreeMap/BTreeSet、BinaryHeap）；str、slice、Iterator、Option、Result、char、数值类型的常用方法；fs、io（BufRead、Seek、Cursor、ErrorKind）、env、process（posix_spawn、管道）、std::os 扩展；sync（RwLock、Condvar、Once、OnceLock、LazyLock、Barrier）、原子类型、时间（SystemTime、sleep）。
- 运行时：定时器（最小堆，空闲的 M 睡到下一个定时器）；网络轮询器（kqueue/epoll，边沿触发，一个专门的 M）。
- 网络：std::net（地址、TcpListener/TcpStream/UdpSocket，阻塞只挂起绿色线程）、std::url（url crate，WHATWG 解析）、std::http（类型照 http crate，客户端照 reqwest::blocking，服务端每连接一个绿色线程，路由照 Go 1.22 的 ServeMux；没有 TLS）。Windows 上用 IOCP（同 Go）：socket 以重叠方式打开、关联到完成端口，读写、AcceptEx、ConnectEx 发起后挂起绿色线程，轮询线程从 `GetQueuedCompletionStatusEx` 取完成事件唤醒它；超时用 `CancelIoEx`。
- 照 crate 做的模块：regex（RE2 语义，Pike VM 加有界回溯）、rand（0.9，同一个种子的值和 crate 相同）、chrono、base64、hex、crypto（MD5、SHA-1、SHA-2、HMAC）、hash::{crc32, adler32}、compress（自己实现的 DEFLATE，flate2 的 API）、log（log 加 env_logger）、cli（clap 4 的 builder API）、csv。
- 验证方法：能拿到参照实现的都做了逐字对照——regex 对 Go 的 regexp（手写用例加 3600 个随机模式），rand、chrono、clap、csv 对本机缓存的 Rust crate，编码、哈希、压缩对 Python 的 hashlib/base64/zlib/gzip 和系统 gzip。和参照不同的地方都记在 spec/std-diff.md 第 9、10 节。
- 顺带修的编译器问题：
  - std 函数体是按需检查的，检查出的错误以前被丢掉（代码照样生成）。现在会报错，`NAVI_CHECK_STD=1` 检查 std 的全部函数体；完整验证在五个目标上都跑这一项。用它找出并修了 std 里十几处一直没报出来的类型错误。
  - trait 选择：嵌套约束的参数是未知变量时直接算作有歧义（rand 的自引用 impl 曾让检查指数爆炸）；投影在只有一个 impl 可匹配时直接用它（`for i in 0..3 { i as u8 }` 以前报错）。
  - 整数字面量：超过 64 位的 128 位字面量（以前静默变成 0）、`-128i8` 这类取负的字面量、超出类型范围的字面量报错；int128 降级漏掉转换之后的运算。
  - `x.clone()` 在 `x: &T` 或 `&mut T` 时克隆 T（以前 `&mut T` 克隆的是引用，std 里两次因此出错）；T 没有 Clone 时报错（比 Rust 对 `&T` 只给警告更严）。
  - 模板字符串的格式说明按原文处理（`${x:08x}` 以前报错）；单元结构体、常量可以按 `&mut` 借用为临时值；arm64 上 C 调用超过 8 个参数。
  - std：`RangeInclusive` 到类型最大值不再溢出、可以反向迭代，char 区间跳过代理区；`AsRef` 照 Rust 对引用通用实现。
- 已知的不足：
  - regex 的 Pike VM 和 compress 仍比 Go/C 慢很多。内联加强后，热点已经是 VM 和编码循环本身的代码质量（后端），见下一节。
  - 没有 TLS；Windows 上 fs/process 的 Unix 专有部分没有测试；Windows 上 ConnectEx 要先 bind（同 Go），4 分钟内关掉的出站连接超过约 16000 个会用尽临时端口。

## 内联加强（2026-11-18）

- 原因：内联器门槛是"3 个块、6 条语句"，而且排除了三类调用：
  - `#[track_caller]` 函数；
  - trait 方法调用。`v[i]` 是 `Index::index`，要到 plan 阶段才定到 impl；
  - 只含 `goto` 的空块和不可达块也算进大小。

  所以 `Vec` 下标从来没有被内联。
- 改动：
  - 新增 `mir/simplify.navi`：跳过空跳转块、单前驱的后继并入前驱、删掉不可达块。内联前后各跑一次。
  - 具体类型上的 trait 方法调用，先用 `resolve_method` 解析到 impl 方法再内联。默认方法、内建实现和动态派发不处理。
  - `#[track_caller]` 函数可以内联：复制的代码里所有位置都改成调用点，正好是它们要报告的位置。
  - `#[inline]` 的函数预算放宽到 24 条语句、6 个块。默认预算仍是 6/3：全局放大预算会让自举变慢 15% 到 60%，二进制涨到 17 到 22 MB。
  - std 里给 `Vec` 的 `index`/`index_mut`/`get`/`get_mut`/`push`/`pop`、切片的 `get`、`Option`/`Result` 的小方法标了 `#[inline]`；`Vec::push` 只在满了时才调用 `reserve`。
- 结果（release，M 系列 Mac）：

  | | 之前 | 之后 |
  |---|---|---|
  | regex 查找 / 捕获 | 488 / 705 ms | 约 405 / 595 ms |
  | 压缩 / 解压（级别 6，10 MB） | 443 / 142 ms | 约 284 / 112 ms |
  | 编译器自举 | 2.33 s | 2.31 s |
  | 二进制 | 12.1 MB | 13.4 MB |

- 剩下的时间在 regex VM 和 DEFLATE 循环本身：后端的寄存器分配和指令选择。
- 顺带修了两处：
  - 构建缓存的键加上输出文件名。以前会复用用别的名字链接的可执行文件，macOS 签名里的标识符就是那个旧名字。
  - `tools/navictest.sh` 检查 `// stderr:` 行（这些文本必须出现在标准错误里）。

## 后端优化与 SSA 后端（2026-11-18）

内联之后，regex 和 DEFLATE 剩下的时间都在后端生成的代码本身。这一轮分两步。

**第一步：在原有的 MIR 和后端上修补**（各自都做了全量验证，已合并到 main）：
- `mir/copyprop.navi`：复制传播和死赋值删除。只赋值一次、没被借用的局部变量，直接用它的值替换。
- 后端：算术和比较直接用寄存器里的操作数；断言（边界检查）和比较融合成 cmp + b.cond；arm64 上离 FP 超过 ±256 的帧槽改由 SP 寻址。
- 胖指针（切片、`&str`、`&dyn`）放进两个寄存器；元素地址用 base + index << k 一条指令算出。

| | 之前 | copyprop | 后端修补 | 胖指针 |
|---|---|---|---|---|
| regex 查找 / 捕获 | 395 / 558 ms | 304 / 444 | 243 / 343 | 226 / 329 |
| 压缩 / 解压 | 282 / 106 ms | 234 / 91 | 189 / 85 | 169 / 69 |
| 二进制 | 13.37 MB | 11.79 | 10.37 | 10.18 |

**第二步：照 Go 换成 SSA 后端**（设计和各阶段见 `docs/ssa.md`）。用户问"为什么不做 SSA"，随后决定整个后端换成 SSA。
- 实现：
  - `compiler/src/ssa/`：IR、支配树、校验器、CSE、简化版 prove、准备 pass、线性扫描寄存器分配；
  - `backend/ssagen.navi`：从 MIR 构造 SSA；
  - lower.navi 末尾：从 SSA 生成代码。
- 调用约定和 runtime 约定不变；内建函数、闭包、defer、C 调用经桥接区复用旧后端的 helper。
- 现在是唯一的后端：旧的逐语句翻译（及其寄存器分配）已经删除，arm64 和 x64 上所有函数都走 SSA。
- 全量验证全部通过：自举、selfcheck、确定性、5 个目标、arm64、x64（Rosetta）和 Android 上的各种压力模式。
- 运行速度（arm64，SSA 对比旧后端）：regex 查找 / 捕获 230/316 → 约 152/217 ms，压缩 167 → 约 128 ms，二进制 10.18 → 9.40 MB。`while i < v.len() { v[i] }` 这类循环的边界检查由 CSE 和分支事实消掉了。
- 编译速度：
  - SSA 构造、优化、寄存器分配都是新增的工作。一开始 10 万行冷编译从 2.68 s 变成 3.35 s（+25%）。
  - 照 Go 的 ssa.Cache 改造后，交替测 5 次的平均值是 2.76 s 对 2.90 s（+5%），改一个函数体后的重编译相同。具体做法：
    - 每个线程复用 Value 和 Block 对象以及寄存器分配的缓冲区；
    - CSE 的表改成数组；
    - 调用目标只解析一次；
    - 块末定义和支配树孩子改成扁平数组；
    - `Vec::resize` 先 reserve，对按位可复制的类型直接写入；
    - 切片迭代器的 `next` 标为 `#[inline]`。

    这些改动之前，SSA 路径约三分之一的时间花在 GC 写屏障和分配上。
  - `NAVI_SSATIME=1` 打印各阶段的 CPU 时间。
- 随后补完：
  - 块布局；
  - 循环不变量外提；
  - 浮点进 FP 寄存器（Mandelbrot 4.6 倍）；
  - CTX/RET/LOC 参与分配（x64 也有了可用寄存器）；
  - 基本类型上的内建比较和 clone 直接成为运算，加上第二轮内联和 `#[inline(always)]`（`for i in 0..n` 不再有调用）；
  - 聚合局部变量拆分（引用转发加构造器拆分；`for` 循环与 while 循环一样快）；
  - 删除旧后端（lower.navi 从 5100 行减到 3700 行）。
- 再后来：
  - 跳转线程化，拆分来自调用结果的局部变量，`for i in 0..v.len()` 的边界检查消失；
  - 归纳变量的下界和不等式事实，`v[i - 1]` 这类下标的边界检查也消失；
  - 直接调用的寄存器传参（regex 约快 10%）。
- C 调用（FFI），照 Go 的 cgo：
  - 调用 `extern "C"` 函数期间，绿色线程算作"阻塞"（`enter_c`/`exit_blocked`，由 MIR 包在调用两边），GC 不等它返回。原来 C 里阻塞 2 秒，其他线程跟着停 1.9 秒，现在是 0。
  - 按阻塞处理的调用切到系统线程的调度栈上运行（`call_c` 的 sys，`G.c_stack`/`G.c_sp`），不再受绿色线程栈底 32KB 的限制。标了 `#[nonblocking]` 的不包、不切栈：每次约 1.4 ns，按阻塞处理的约 12 ns（arm64，单线程）。std 里会阻塞的函数（read、write、open、waitpid、poll、getaddrinfo 等）单独声明，其余都标 `#[nonblocking]`。
  - C 回调 navi：有函数体的 `extern "C" fn` 多生成一个 C 入口（`sym.c`，`Machine::c_callback`），它的值就是这个入口。入口经线程局部变量（pthread key / TlsAlloc，`_navi_tls_key`）找到 M，在 M 的回调 G 上运行（`cb_enter`/`cb_exit`，回调 G 在第一次阻塞调用时创建；回调里再调 C、C 再回调时用下一层）。发起调用的 G 一直阻塞、栈不动，所以回调里栈可以增长和搬动。回调里等待锁或 channel 时系统线程睡眠（`stop_callback`，同 Go 回调期间锁定线程）。
  - 监控线程（`sysmon`，第一次 spawn 时启动）：某个 M 的 G 阻塞了一整个检查周期（20µs 起，空闲时退到 10ms）又有活要干，就让另一个 M 接手（`retaken`，`NRETAKEN`）；它回来后，运行中的 M 超出上限时，`find_runnable` 让一个去空闲。`NAVI_MAXPROCS=1` 时阻塞的 C 调用也不再卡住其他线程。
  - 测试：`tests/run/c_blocking`、`tests/run/c_callback`（qsort 回调里分配和 GC、栈增长、嵌套回调、浮点、回调里等待其他线程、8 个线程同时回调），arm64 各种压力模式和 x64 都通过。
- `#[link(name, kind = "dylib" | "framework")]`：`extern` 块的函数从指定的动态库导入（resolve 记在 `Def.link`，目标文件记在 `Object.libs`，`obj::lib_file` 把名字换成各平台的文件）。Mach-O 每个库一条 LC_LOAD_DYLIB、按库序号绑定；ELF 加 DT_NEEDED；PE 每个 DLL 一个导入目录。`kind = "static"` 报错：navi 不链接外来的目标代码，所以从不需要系统链接器（spec 1.16）。`NAVI_LINKER=cc` 时作为 `-l`、`-framework` 传下去。macOS 上 libz 和 CoreFoundation 在 arm64、x64、cc 三条路上都验证过；Linux 和 Windows 的产物用 llvm-readobj 检查过 DT_NEEDED 和导入表。
- 构建缓存的淘汰（同 Go）：5 天没用过的条目删掉；写入缓存的构建最多一天清理一次（`trim.txt` 记上次的时间）；命中时把条目标为刚用过（最多一小时一次）。原来缓存只增不减，这几天的压测把它涨到了 15GB。
- 编译器的偶发死锁（`all goroutines are asleep`），已修：
  - 原因：类型查询的可重入锁把持有者记在 `Option<ThreadId>` 里，不加同步地读写。刚释放锁的线程可能读到下一个持有者写了一半的值（新的 Some 标签加上自己的旧 id），以为自己持有锁而跳过加锁，之后再替真正的持有者解锁，所有工作线程于是都等在锁上。现在持有者是一个原子字（`ThreadId::as_u64`）。
  - 在 GitHub CI 的 macos-x64 机器上出现了两次，靠下面的 goroutine 现场打印定位。
  - 之前的复现尝试：用 main 和当前编译器各编译约 1360 次，带诊断的编译器 5 路并行又编译 6450 次（其中 2580 次与出事时配置相同：x64 目标、`NAVI_MAXPROCS=4`），模拟 `par_for` 的调度器压测程序 9000 次，都没有复现。
  - 读过 channel、select、Mutex、wait list、空闲 M 管理和 STW，没找到丢唤醒的路径。
  - 现在 runtime 在死锁时会列出每个没结束的 G 和它各帧的返回地址（同 Go 列出 goroutine），`tools/symbolize.py 程序 < 输出` 可以换成函数名；下次出现时有现场可查。
- GitHub Actions（github.com/sunli829/navi-lang）：
  - linux-x64、linux-arm64、macos-arm64、macos-x64 上从 stage0 自举到定点，再在四种运行时模式下跑 golden 测试，全部通过。顺带修了 glibc 的 `cbrt` 不精确（改成 navi 自己算）和多个定时器同时到期时的唤醒顺序。
  - Windows 上用交叉编译的 `navic.exe` 跑测试，全部通过（CI 失败时用 cdb 打印崩溃现场，`NAVI_LINKMAP` 让 PE 链接器写出符号地址）。修过的：C 返回值窄于寄存器时的扩展；amd64 在 Windows 上大于 4KB 的栈帧逐页探测（静态变量的初始化跑在 OS 栈上，用到 crc32 的程序在 main 之前就崩溃）；fmt 和 lsp 读标准输入不再用 `/dev/stdin`，lsp 认得 `file:///C:/...`；`.gitattributes` 固定 LF（Windows runner 默认 autocrlf，逐字节比较的测试都失败）。Windows 上的网络用 IOCP 实现，`net`、`http` 也通过（1000 个同时挂起的连接只用 9 个 OS 线程）；顺带查出轮询线程的一个老问题（Unix 也有）：它传给内核的缓冲区是局部变量，逃逸分析给了堆单元，分配在没有 GC 根的调度器记录上，被回收复用后内核和轮询线程还在写（Windows 上用 64KB 的 Op、GC 频繁时必现），现在是静态变量；`env_process`、`fs_ops`（Unix 专有的 API）在 Windows 上跳过（`// skip: windows`）。顺带修了解析器：`fn` 的名字写成关键字（如 `fn select(...)`）时报错后跳过它，不再把参数解析错位、让后面的阶段死循环。
- 删掉了 stage0（Go 写的原型编译器）和所有 Go 代码：`internal/`、`cmd/`、`go.mod`、`stdlib.go`，生成器 `tools/gennum`、`tools/genregex`（它们生成的 std 文件改为手工维护），编译速度基准 `bench/` 和 `tools/bench.sh`（拿 `go build` 作对照），`tools/selfcheck.sh`（拿 navic 和 stage0 的输出对照）。
  - 自举从上一个 release 开始（`tools/bootstrap.sh`，`tools/bootstrap-version`）；规则同 Go：编译器和 std 只能使用那个 release 支持的功能。
  - std 里给 stage0 的 `#[cfg(not(feature = "navic"))]` 分支和 `#[cfg(feature = "navic")]` 标记都去掉了（约 1200 行），编译器里的 `NAVI_STAGE0` 模式（模仿 stage0 的编号，用来对照输出）也去掉了；`feature = "..."` 现在一律为假。
  - 原来由 Go 测试驱动的前端用例改由 `tools/navictest.sh` 跑：`tests/syntax` 要能解析，`tests/resolve`、`tests/types`、`tests/check` 的 `ok`/`sigs` 没有错误、`err` 的错误和 `// error:` 标注一一对应（`// type:`、`// sig:` 这两种更细的断言没有沿用）。测试里的 `// stage0: skip`、`// interp: skip` 标记去掉了。
- v0.0.2 进行中：
  - `navic` 改名为 `navi`（`tools/navictest.sh` 改为 `tools/test.sh`；`tools/bootstrap.sh` 仍认得 v0.0.1 包里的 `bin/navic`）。
  - 依赖（`compiler/src/deps.navi`，spec 1.10）：`navi.toml` 的 `[dependencies]`，本地路径或 git（tag、rev 或默认分支），以源码编译；同一仓库的不同版本并存（选择同 Cargo），第二个版本命名为 `名字@版本`，类型名相同而 crate 不同时报错信息带上 crate；`navi.lock` 记录 git 依赖的 commit，`navi update` 重新解析；检出在缓存的 `pkg/`（`NAVI_PKG`），由系统 git 拉取（先按 commit 浅拉取，不行再全量）；语言服务器只用已拉取的；依赖循环和非库依赖报错。`tools/test.sh` 用本地临时 git 仓库测这些（两版本并存、tag 挪动后锁住、update、类型报错）。
  - 测试改成自检：`tests/run` 里 80 个用例改成 `#[test]` 函数加 `assert_eq` / `assert`（格式化按字符串比较），由 `navi test` 运行（`tools/test.sh` 对没有 `// out:`、`// exit:`、`// stderr:`、`// warn:` 的文件用 `navi test`）；输出本身就是被测对象的（println、panic 和退出码、警告和错误、stdout 日志、要用 main 参数的）保留输出比对。
  - 模板字符串的类型总是 `fmt::Arguments`（spec 1.13，相当于 Rust 的 `format_args!`），不再随上下文变成 `String`；要 `String` 时显式写 `` `...`.to_string() ``（相当于 `format!`），编译器对这个写法直接生成字符串，不经过 `Arguments`。惰性：`${}` 在创建时求值，写出时才格式化。编译器、std、测试里约一千处补上 `.to_string()`（这个写法 v0.0.1 也能编译，自举不受影响）。`tools/test.sh` 的前端用例在没有 `// error:` 标注时也能报出多余的错误（以前 awk 把空的期望文件跳过，错误被当成期望）；依赖测试失败时报 FAIL，不再因为 `set -e` 直接退出。
  - `assert_eq` / `assert_ne` 的两边可以是不同类型（`A: PartialEq<B>`，同 Rust 的宏），测试里比较字符串不再需要 `.as_str()`。std 的约束按 Rust std 的习惯写（spec 1.14 第 10 条）：多个约束或约束里引用另一个类型参数时用 `where`（78 处）。Windows 上编译器去掉规范路径的 `\\?\` 前缀（std 的 `canonicalize` 同 Rust 返回 `\\?\C:\dir`，再用 `/` 拼接就找不到文件：依赖的 navi.toml 读不到，release 的 navi.exe 也找不到旁边的 lib/std）。

## 下一步

1. 冷编译提速：剩下的串行部分（plan 的按序遍历、codegen 合并之后的 gen_data、链接）；GC 标记占约四分之一 CPU（保守扫描，生成的代码不做寄存器分配）；内存占用（GC 节奏）；std 的导出数据缓存；函数粒度的增量缓存。
2. 运行时：栈收缩、最后一次 STW 的并行排空。
   - 抢占（2026-11-21，用户选了回边检查而不是 Go 的信号异步抢占：GC 和栈搬移只在调用点有精确栈图）：每个循环头检查 G 的栈界限（amd64 `cmp rsp, [r14]; jb`，arm64 `ldr; cmp sp; b.lo`，同 JVM 的 safepoint poll），失败时调用共享的 `_navi_poll`：保存 SSA 分配的浮点寄存器，压入"循环的 FP + 返回地址"帧记录，以 need 0 进入 morestack，只停不搬栈（collector 按函数的栈图扫描这一帧）。`std::rt` 里的函数不检查（运行时依赖某些指令之间没有停止点）。死循环不再卡住 STW（`tests/run/preempt.navi`：GC 原来要等循环结束，约 6 秒，现在 50 毫秒）。代价：编译器自身构建测不出差别，体积多 0.8%；最紧的循环（5 条指令的求和）慢约 6～8%，排序、正则约 0～3%。
   - 下一步：按时间片抢占（sysmon 发现 G 连续运行超过 10ms 就请它在下一个检查点让出，同 Go）。
3. DWARF 的变量和类型信息（三种格式现在都只有函数和行号）。
4. v0.0.2：依赖、改名 `navi`、自检测试都已完成（见上），另外做了模板字符串总是 `fmt::Arguments`、`assert_eq` 两边可以不同类型、std 约束的 `where` 写法。已发布。
5. v0.0.3（项目管理，照 Cargo；构建模式照 Go）：已发布（tag 在 727a407），自举起点改为 v0.0.3。
   - 已完成：项目目录必须有 `navi.toml`（`[package]` 的 `name` 必填），从当前目录往上找；单个 `.navi` 文件仍然可以直接编译。`navi.toml` 和 `navi.lock` 由真正的 TOML 解析器读（`compiler/src/toml.navi`，`manifest.navi`），不认识的表和键报错；项目和 workspace 在 `project.navi`，依赖图以整个 workspace 为单位加载（`deps.navi`）。编译器自己的单元测试（`#[cfg(test)]`，`navi test compiler`）和项目场景都进了 tools/test.sh。
   - workspace：根目录 `navi.toml` 的 `[workspace]`（`members` 支持 `*`、`exclude`）和 `[workspace.dependencies]`，成员写 `name = { workspace = true }`，路径相对于 workspace 根目录；整个 workspace 共用根目录的一个 `navi.lock`；成员往上找到列出自己的 `[workspace]`；根目录可以只有 `[workspace]`，也可以同时是一个 crate。在根目录运行 `navi build` / `navi test` 处理全部成员，`-p name` 选一个；在成员目录里只处理这个成员。
   - 输出放在 workspace（或项目）根目录的 `target/名字`，`-o` 仍可指定。
   - `navi new 名字` / `navi init`（`--lib` 建库）：写 `navi.toml` 和 `src/main.navi`（或 `src/lib.navi`），`git init` 并写 `.gitignore`（忽略 `target/`）；在 workspace 里创建时自动加进 `members`。
   - `navi run [-p name] [-- 参数]`：构建并运行，也能 `navi run x.navi`。
   - 测试照 Rust：已完成大部分。`navi test` 支持 `#[should_panic]` / `#[should_panic(expected = "...")]`，运行测试时设置 `NAVI`（同 Cargo 的 `CARGO`）；tests/run 全是 `#[test]`：panic 用 `should_panic`，未捕获的 panic 和致命错误（死锁、栈溢出）由测试用 `NAVI` 编译一个小程序再检查退出码和输出（同 Go 的 runtime 测试），defer 顺序和 track_caller 位置用 `catch_unwind` 断言，env_process 的子进程是测试程序自己（`current_exe` 加名字过滤）；`// skip: windows` 换成 `#![cfg(not(target_os = "windows"))]`。编译错误和警告的测试在 `tests/ui/`，前端测试同样用 rustc 的 `//~ ERROR` / `//~ WARN` / `//~^` / `//~|` 标注。tools/test.sh 不再有 `// out:` / `// exit:` / `// stderr:` / `// skip:`。`assert_msg` 加进 prelude。修了一个老问题：结果没被使用的带检查运算（`let _ = x + 1`）被 SSA 的死代码删除去掉，溢出不再 panic（现在可能 panic 的运算不删）。剩下项目的集成测试目录（`tests/*.navi`、`NAVI_BIN_EXE_名字`），和项目管理一起做。
   - 只有一种构建模式（同 Go）：已完成（`--release`、`cfg(debug_assertions)`、`debug_assert` 去掉，溢出一律检查，`navi build -s` 去掉 DWARF）。
6. 平台缺口（2026-11-20 排查：Windows 最多，Linux 几处，macOS 没有独有的）：
   - 已完成：Windows 的字符串都走 UTF-16 的 `*W` 函数（路径、环境变量、`CreateProcessW` 和 UTF-16 环境块、`GetCommandLineW` 按 C 运行时规则拆参数、控制台输出 `WriteConsoleW`），`temp_dir` 用 `GetTempPathW`，`current_exe` 不限长度，`read_link` 读重解析点（链接本身的内容）；Linux 的 `Metadata::created` 用 `statx`；`str::encode_utf16`。PE 照 Go 的链接器：COFF 符号表、DWARF 段（行号）、`.pdata`/`.xdata` unwind 表（序言 `push rbp; mov rbp, rsp`，序言位置相同的函数共用一份），不做 PDB。Windows 测试：`fs_ops`、`env_process` 在 Windows 上也跑（`sh` 或 `cmd`），新增 `unicode_os`，C 调用测试在 Windows 上用 msvcrt 的 `_snprintf` 和 ntdll 的 `RtlComputeCrc32`，gc 的内存上限用峰值工作集；Windows CI 加上单线程、GC 校验、栈搬移三种模式。`navi test --no-run` 只编译测试程序并打印路径（同 cargo）。
   - 剩下（较小）：Windows 绿色线程栈一次提交、定时器毫秒精度、网络读写经 64KB 缓冲、出站连接端口；Linux arm64 原子操作不用 LSE；Linux 定时器用 CLOCK_REALTIME 绝对时间；`env::vars` 看不到 C 代码加的变量（Linux）；Windows 权限只有只读位、`os::windows` 扩展少、C 回调至多 4 个参数；编译器的 SHA-256 在 Linux/Windows 是纯软件；Android 不在 CI（`tools/androidtest.sh` 可以改用 `navi test --no-run`）。
