# WebAssembly 目标设计（浏览器，照 Go 的 js/wasm）

目标：`navi build --target js-wasm32` 生成一个 `.wasm` 文件，配上 std 自带的胶水 JS（`navi_wasm.js`），在浏览器里运行。只面向浏览器，不支持 Node（不提供 Node 下的文件系统、子进程等）。语言、std 的 API 和运行时的约定都不变：GC、可增长的栈、绿色线程、channel 和 `select`、`defer`、panic 和 `catch_unwind` 照常可用。另外新增 `std::js`，对应 Go 的 `syscall/js`，用来访问 JS 的值和 DOM。

整体照 Go 的 js/wasm 做（`cmd/compile/internal/wasm`、`runtime/*_wasm.*`、`syscall/js`、`lib/wasm/wasm_exec.js`）。原因是 navi 的运行时本来就是照 Go 写的：
- 精确的栈图；
- 沿帧指针链扫描和复制的栈；
- 协作式抢占；
- G 和 M 的调度。

Go 在 wasm 上解决的问题，正好就是 navi 会遇到的问题。

## 1. 约束：wasm 和原生的差别

| 原生平台上运行时依赖的 | wasm 里的情况 |
|---|---|
| 栈在普通内存里，可以读、扫描、复制、切换（`swap_context`、`setjmp`/`longjmp`） | wasm 自己的调用栈不可见，不能扫描，也不能切换 |
| 任意跳转 | 只有结构化控制流：`block`、`loop`、`if`、`br`、`br_table` |
| G 寄存器（x28 / r14）、SP、FP | 没有寄存器，只有局部变量和全局变量 |
| 线程 | 浏览器主线程只有一个，而且不能阻塞（阻塞会卡住页面） |
| libc（`mmap`、`write`、`clock_gettime`……） | 只有宿主（JS）导入的函数；内存只能用 `memory.grow` 增长，不能归还 |
| 机器码、Mach-O/ELF/PE | wasm 模块（类型、导入、函数、表、内存、数据段） |

## 2. 原则

- **运行时的数据结构和约定不变**。栈帧还在线性内存里，帧布局（FP 链，FP-8 到 FP-32 的固定槽位）、栈图、`_navi_ftab`、G 的结构全都照旧。所以下面这些代码基本不用改：
  - GC 的栈扫描、栈的增长和复制（`move_frames`）；
  - panic 沿 FP 链展开、运行 defer；
  - 调度器的队列、channel、`select`、定时器堆。
- **代码生成复用 SSA 和寄存器分配**。新写一个 `Machine` 的实现（`backend/wasm.navi`），和 arm64、amd64 并列。lower.navi 和 ssa/* 保持与架构无关。
- **绿色线程用"可恢复函数"实现，照 Go 的做法**：调用之后检查一个标志，需要切换时，整条 wasm 调用栈逐层返回；之后再从线性内存里的栈帧恢复。不依赖还没普及的 wasm 栈切换提案（JSPI / stack switching），这样各浏览器都能跑。
- **单线程**。只有一个 M，所有 G 都在它上面跑。没有可运行的 G 时，把控制权交还给 JS 的事件循环，等定时器或 JS 事件再回来。

## 3. 目标名和 cfg

- 目标名：`js-wasm32`。`--target js-wasm32`，加进 `driver::target_by_id`。
- cfg 的值：
  - `target_os = "js"`（Go 的 GOOS=js）；
  - `target_arch = "wasm32"`（Rust 的写法）。
- 需要改 `driver::target_arch()`：现在它把所有不是 arm64 的目标都当成 amd64。
- 后端选择：`backend()` 用 `wasm::new_machine()`，`object()` 和 `native()` 走新的 `wasmlink`（第 9 节）。

## 4. 内存和指针

- **指针还是 8 字节，照 Go 的 GOARCH=wasm**。`usize`、指针和 `layout.navi` 都不变。
  - 地址的值都小于 4 GiB。
  - 访问内存时先 `i32.wrap_i64` 再 `i64.load` / `i64.store`。
  - 这样类型布局、derive、GC 的指针位图、栈图全都不用为 wasm 改，代价是每次访问内存多一条指令。
- **另一个选项是 memory64**（Wasm 3.0 里的 64 位线性内存），可以省掉这条转换。但各浏览器的支持情况还要确认（尤其是 Safari），先不用。后端只在一个地方生成地址，以后切换不难。
- **线性内存的布局**（从低到高）：
  1. 0 到 4 KiB 留空，让空指针访问容易发现。注意 wasm 读写地址 0 本身不会出错，所以空指针检查还是要靠编译器生成的检查和 Option 的表示。
  2. 数据段（rodata 和 data，链接时确定地址）。
  3. 初始 G（g0）和它的栈。
  4. 堆。

## 5. 代码生成（backend/wasm.navi）

### 5.1 寄存器和函数形状

- **寄存器**：`Machine` 的寄存器（RA、RM、V1、V2、T1、BASE、CTX、RET、LOC、A1……、FA1……）各对应一个 wasm 局部变量，整数用 i64，浮点用 f64。SP 和 G 是全局变量（`global $sp`、`global $g`，Go 也是这么做的）。
- **寄存器分配**：`ssa::regalloc` 的 `Config` 给 wasm 一组"寄存器"，也就是局部变量，数量可以多给，比如 16 个整数、16 个浮点；但**没有被调用者保存的寄存器**。这样跨调用活着的值都在线性内存的栈帧里，函数可以从任何调用点恢复（5.3）。
- **函数形状**：每个 navi 函数是一个 wasm 函数，签名统一为 `(resume: i32) -> i32`。
  - 参数和结果照现有的内部 ABI 放在线性内存的栈上；CTX、RET、LOC 通过全局变量传递。
  - 返回值 1 表示"要让出，wasm 栈逐层返回"，0 表示正常返回。

### 5.2 控制流：块号分派

照 Go：函数体是一个 `loop`，里面嵌一组 `block`，开头用 `br_table` 按块号跳到对应的块。
- `Machine::new_label` / `bind` 分配块号，`jump(l)` 写成"设块号，`br` 回循环开头"。
- 条件分支先算条件，然后 `if` 里跳。
- 只在**必须**的地方开新块：跳转目标和调用之后的恢复点。顺序执行的代码留在同一块里。

这个做法简单，能表示任意控制流，也天然支持"从块 N 恢复"。代价是跳转比原生慢，wasm 引擎也不好优化。以后可以对不跨恢复点的部分改用结构化的发射方式（stackifier）。

### 5.3 调用和恢复

- **调用 f**：
  1. 在栈上写"返回地址"，也就是 `PC = 函数号 << 16 | 恢复块号`，模拟 call 指令压栈；
  2. 执行 `call $f`；
  3. 返回值不为 0 时，当前函数也返回 1（继续向外让出）；
  4. 为 0 时跳到恢复块继续执行。
- **函数指针和 vtable** 里存的是"函数号 << 16"，`call_reg` 用 `call_indirect`（表下标是 PC >> 16）。
- **让出**（切换 G、栈增长、`longjmp`）：
  1. 把 SP 等存进 G；
  2. 设好要恢复到的 G 的 SP；
  3. 返回 1。wasm 栈一路返回到最外层的运行循环 `run_loop`（Go 的 `wasm_pc_f_loop`）。
- **恢复**：`run_loop` 从当前 G 栈顶读出 PC，按函数号 `call_indirect` 调用它，传入块号，函数从那个块继续。函数正常返回时，同样由 `run_loop` 读下一个返回地址，接着恢复调用者，直到又回到一个 wasm 帧还在的地方。
- 这样下面这些都在线性内存里完成，不需要看见 wasm 栈：
  - `swap_context`、`set_jmp` / `long_jmp`、`mstart`、`morestack`（复制栈后从函数开头重新执行）；
  - `poll`（循环回边的抢占检查）。

### 5.4 栈图

`_navi_ftab` 里的"代码偏移"改成上面的 PC（函数号 << 16 | 块号）。
- `Machine::offset()` 返回当前块号，所以 `stack_meta` 和 `finish_meta` 不用改。
- 调用点的返回地址就是恢复块的 PC。
- `sched::find_map` 照旧二分查找；`CODE_UNIT` 在 wasm 上为 1。

### 5.5 指令

- **整数运算**：用 i64 指令。溢出检查用比较实现（加减法用符号位公式）。wasm 没有乘法高位指令，有符号和无符号乘法的溢出检查调用运行时的辅助函数（int128.navi 里已有类似代码）。
- **除法**：除以零和 `MIN / -1` 在 wasm 里会直接陷入（trap）。navi 本来就在除法前检查，照旧生成检查即可。
- **浮点转整数**：用 `trunc_sat` 指令，饱和语义正好和 navi（Rust）的 `as` 一致。
- **原子操作**：单线程，直接用普通的读写。
- **`#[asm]` 函数**：没有 wasm 版本。加密库已经有纯 navi 的后备实现（`cfg(not(any(target_arch = "aarch64", target_arch = "x86_64")))`），直接用。
- **name 段**：写入函数名，浏览器开发工具的调用栈能看到 navi 的函数名。DWARF 以后再说。

## 6. 运行时（std::rt）的改动

| 部分 | 改动 |
|---|---|
| 入口 | 导出 `run(argc, argv)`：初始化 g0、堆、主 G，进入 `run_loop`。没有 `gen_main` 那段原生入口代码 |
| M 和线程 | 只有一个 M，不创建线程（`pthread_create` 的路径在 js 上不编译）。没有 sysmon |
| 调度 | 现有的队列和窃取代码照用（只有一个本地队列）。没有可运行的 G 时：有定时器就让 JS 设 `setTimeout`，然后返回 JS；JS 回调时调用导出的 `resume()` 继续 |
| 抢占 | 和 Go 的 wasm 一样只有协作式：在函数调用和 channel 操作处切换，循环回边不检查时间。纯计算的长循环会占住页面，直到它让出 |
| 内存 | `mmap` 换成 `memory.grow`。arena 从 64 MiB 改为 4 MiB（Go 在 wasm 上也是 4 MiB）。内存不能还给宿主，scavenger 在 js 上什么也不做。GC 元数据原来用 `calloc` 分配，改为从 arena 里分配 |
| GC | 标记工作者本来就是 G，单线程时在 G 停下时运行（`start_cycle` 里已有这种处理）。写屏障、标记辅助、清扫、步调控制都照旧。根：栈（在线性内存里，精确扫描）和数据段 |
| 栈 | 初始和最大大小改小（4 GiB 的地址空间里，最大 1 GiB 太大，比如改成 256 MiB） |
| 定时器、channel、select | 不变。`sleep` 停放 G，定时器到期时由 `resume()` 唤醒 |
| C 调用 | 没有 `call_c` 和切换到系统栈那一套。`extern "C"` 在 js 上编译成 wasm 导入（第 7 节），直接调用 |
| netpoller | 不编译 |
| panic | FP 链展开照旧；`catch_unwind` 的 `longjmp` 用 5.3 的让出机制实现 |
| 时钟、随机数、输出、退出 | 换成宿主导入（第 7 节） |

## 7. 宿主接口和胶水 JS

wasm 模块从模块 `"navi"` 导入下面这些函数，参数和返回值都是 wasm 的 i32、i64、f64：

- `write(fd, ptr, len)`：按行缓冲，写到 `console.log` / `console.error`；页面可以换成自己的输出函数（测试页把输出发回 `navi test`，第 11 节）。
- `exit(code)`。
- `nanotime() -> i64`（`performance.now`）、`walltime() -> (sec, nsec)`（`Date.now`）。
- `schedule_timeout(ms) -> id`、`clear_timeout(id)`：到期后调用 `resume()`。
- `random(ptr, len)`（`crypto.getRandomValues`）。
- `std::js` 用的那一组：`value_get`、`value_set`、`value_index`、`value_call`、`value_invoke`、`value_new`、`value_length`、`value_prepare_string`、`value_load_string`、`value_instance_of`、`copy_bytes_to_navi`、`copy_bytes_to_js`、`finalize_ref`。这些对应 Go 的 `syscall/js.*` 导入。

胶水 JS 放在 `lib/std/misc/wasm/navi_wasm.js`（对应 Go 的 `wasm_exec.js`），只用浏览器的 API，负责：
- 实例化模块（`WebAssembly.instantiateStreaming`），传入参数和环境变量（由页面给出）；
- 维护 JS 值的引用表（navi 侧持有的是下标）；
- 驱动 `run()` 和 `resume()`。

`navi build --target js-wasm32` 输出 `.wasm`，同时把 `navi_wasm.js` 和一个最小的 `index.html` 放在旁边。

用户自己的 `extern "C"` 块在 js 上变成 wasm 导入，模块名取 `#[link(wasm_import_module = "...")]`（Rust 的写法），默认是 `"env"`。导入函数的参数照 wasm 的值类型直接传。

## 8. std::js（照 Go 的 syscall/js）

```rust
use std::js::{self, Value, Func};

let doc = js::global().get("document");
let p = doc.call("createElement", &[Value::from("p")]);
p.set("textContent", "hello");
doc.get("body").call("appendChild", &[p]);

let cb = Func::new(|this: Value, args: Vec<Value>| -> Value {
    println(`clicked ${args[0].get("clientX").int()}`);
    Value::undefined()
});
doc.call("addEventListener", &[Value::from("click"), cb.value()]);
```

- **`Value`**：
  - 读写：`get`、`set`、`index`、`set_index`、`length`；
  - 调用：`call`、`invoke`、`new`；
  - 取值：`int`、`float`、`bool`、`string`、`truthy`；
  - 判断：`type_`、`is_null`、`is_undefined`、`instance_of`；
  - 构造：`Value::null()`、`Value::undefined()`、`From<数字/bool/&str/String>`；
  - 字节数组：`copy_bytes_to_navi` / `copy_bytes_to_js`。
- **引用和回收**：`Value` 持有 JS 引用表的下标，用 `gc::add_cleanup` 在被回收时释放（Go 用的也是 finalizer）。
- **`Func`**：把 navi 闭包包成 JS 函数，`release()` 释放。
  - JS 调用它时，胶水 JS 设好事件，再调用 `resume()`。navi 侧有一个处理事件的 G 去运行闭包，闭包返回后 `resume()` 才返回，所以事件处理函数里可以调用 `preventDefault`。
  - 闭包里不能阻塞，否则报死锁，同 Go。
- **等待 Promise**：`Value::await_()`（名字待定）让当前 G 停下，Promise 完成后返回 `Result<Value, Value>`。navi 有绿色线程，fetch、IndexedDB 这类异步 API 可以写成同步的样子，不用回调。Go 没有这个，用户通常自己用 channel 加回调来等。
- 不提供 Go 早期的 `js.Wrapper` 之类的接口。

## 9. 链接和文件格式

- **obj.navi** 加 wasm 的重定位种类：
  - 函数号（`call`、表元素）；
  - 数据地址（i32 和 i64 常量）；
  - 全局变量；
  - 类型号。

  函数体照 wasm-ld 的做法，把要重定位的立即数写成定长 5 字节（或 10 字节）的 LEB128，链接时原地填上。
- **merge.navi** 照旧合并各个 crate 的对象：去掉没用到的函数，裁剪 vtable，重建 `_navi_ftab`。
- **新的 `wasmlink.navi`** 生成模块，各段依次为：
  - type（统一的函数签名，加上各个导入的签名）；
  - import；
  - function；
  - table（函数指针用）；
  - memory；
  - global（SP、G、CTX……）；
  - export（`run`、`resume`、`memory`）；
  - element；
  - code；
  - data；
  - name。
- 不支持 `--linker cc`，也不输出可重定位的 wasm 对象（`navi build` 只出完整模块）。

## 10. std 在 js 上的平台层

| 模块 | js 上 |
|---|---|
| 纯计算的模块：集合、字符串、`fmt`、`iter`、`num`、`json`、`serde`、`csv`、`base64`、`hex`、`url`、`regex`、`compress`、`pem`、`path`（只做拼接和解析）、`derive`、`cli`、`gc`、`bench` | 不变 |
| `crypto` | 用纯 navi 的后备实现（没有汇编加速）；`crypto::x509` 能解析和用给定的根证书验证，不能用系统根证书 |
| `io` 的标准输出和标准错误、`log` | 写到 console |
| `time`、`thread::sleep`、定时器 | `performance.now`、`Date`、`setTimeout` |
| `rand` | `crypto.getRandomValues` |
| `chrono` 的本地时区（`Local`） | 用 JS `Date` 的时区偏移（导入 `tz_offset(sec) -> sec`） |
| `env::args`、`env::var` | 由页面通过胶水 JS 传入 |
| `thread`、`sync`、`mpmc`、`select` | 支持，所有 G 在一个 M 上；`available_parallelism()` 为 1 |
| `process::exit` | 支持 |
| `fs`、`net`（TCP、UDP、DNS）、`http` 的服务端、`tls`、`process::Command`、`io::stdin`、`env::current_dir` / `home_dir` / `temp_dir` 等 | 照常能编译，运行时返回 `ErrorKind::Unsupported`（同 Rust 的 wasm32-unknown-unknown 和 Go 的 js/wasm：跨平台的代码和库不用加 cfg 就能编译）。`stdin` 读到的是空 |
| `http::Client` | 先返回 Unsupported；以后可以用 fetch 实现（Go 的 net/http 在 js 上就是这样），放到 W4 |
| `os` 的 unix、windows 扩展 | 本来就不在 js 上编译 |

`std::sys` 加 `js.navi`，和 unix、windows 并列；`net/sys` 同样处理，里面的函数都返回 Unsupported。

浏览器特有的 API（localStorage、IndexedDB、Canvas、WebSocket……）不进 std，通过 `std::js` 直接调用（第 8 节）。从 WebIDL 生成带类型的绑定（像 Rust 的 web-sys）以后再考虑，不在这次的计划里。

## 11. 测试和 CI

测试直接在无头浏览器里跑，不依赖 Node：

- **`navi test --target js-wasm32`**（`navi run` 同理）：
  1. 编出 wasm；
  2. 用 std 的 HTTP 服务端在本机的随机端口上提供 wasm、胶水 JS 和一个测试页；
  3. 启动无头 Chrome（`--headless=new`，找不到时报错并提示用 `NAVI_BROWSER` 指定）打开测试页；
  4. 测试页把输出和退出码 POST 回服务端，`navi test` 打印输出，然后关掉浏览器。
- **tests/run 的测试**：用到 fs、net、子进程的，用 `#[cfg(not(target_os = "js"))]` 排除，其余都要在 wasm 上通过。这样运行时和代码生成的测试覆盖可以直接复用。
- **CI**：在 ubuntu 上加一个 wasm 任务（GitHub 的 runner 自带 Chrome），跑 tests/run 和 std 的单元测试。
- `std::js` 的 DOM 和回调也用同一个测试页测。

## 12. 阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| W1 | 目标名和 cfg；`backend/wasm.navi`（块号分派、可恢复调用）；`wasmlink`；最小运行时（单 M、`memory.grow`、写输出、退出、时钟）；胶水 JS；`navi run` / `navi test` 的无头浏览器运行器 | hello world、fib、结构体、枚举、闭包、`defer`、panic 带栈回溯，在无头 Chrome 里输出正确 |
| W2 | 完整运行时：GC、栈的增长和收缩、G 的切换、定时器、channel、`select`、`catch_unwind`、事件循环（`resume`） | tests/run 里不依赖 fs、net、子进程的全部在无头 Chrome 里通过；CI 加 wasm 任务 |
| W3 | `std::js`（`Value`、`Func`、回调和事件）；示例页面 | 示例页面能操作 DOM、响应点击；对应的测试通过 |
| W4（以后再定） | `http::Client` 用 fetch 实现 | 对应的测试通过 |
| W5 | 性能和体积：不跨恢复点的代码改用结构化发射，去掉多余的块号分派；必要时做 wasm 专用的窥孔优化 | 基准测试对比 Go 的 js/wasm |

## 13. 已定的选择

1. 目标名 `js-wasm32`，cfg 是 `target_os = "js"`、`target_arch = "wasm32"`。
2. 指针照 Go：8 字节指针，32 位线性内存。
3. 抢占照 Go：只做协作式。
4. 只面向浏览器，不支持 Node；测试也在无头浏览器里跑。
5. 浏览器里没有的 std 功能照常能编译，运行时返回 `ErrorKind::Unsupported`。
6. 浏览器特有的 API 通过 `std::js` 调用；从 WebIDL 生成绑定暂不做。
