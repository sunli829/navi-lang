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
| 自举 | 完成 | `tools/bootstrap.sh`：stage0 → stage1 → stage2 → stage3，stage1 编译自身得到的目标文件与 stage0 相同，stage2 与 stage3 逐字节相同；stage2 编译的 22 个 golden 测试全部通过 |

navic 目前只生成 macos-arm64（Mach-O）；amd64、ELF、COFF 仍由 stage0 提供，按需再移植。navic 编译自身约 48 秒（stage0 约 0.6 秒），生成代码没有寄存器分配、没有内联、所有被取地址的局部变量都装箱，这是 M9 性能工作的起点。

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
  - 再往下：要么给 MIR 瘦身（最大的活数据），要么做精确 GC（只扫指针字）。
- 没做的：
  - 函数粒度的增量缓存（需要序列化类型和 MIR）；现在只有整个构建的缓存。
  - 冷编译与 go build 持平（见上面的数字）。

## 下一步

1. 冷编译提速：剩下的串行部分（plan 的按序遍历、codegen 合并之后的 gen_data、链接）；GC 标记占约四分之一 CPU（保守扫描，生成的代码不做寄存器分配）；内存占用（GC 节奏）；std 的导出数据缓存；函数粒度的增量缓存。
2. 运行时：每个 M 的运行队列、基于信号的抢占、栈收缩、最后一次 STW 的并行排空。
3. DWARF 的变量和类型信息；amd64/ELF/COFF 上的自研链接器和 DWARF（现在只有 macOS arm64）。
