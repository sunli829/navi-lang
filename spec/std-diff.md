# navi std 与 Rust std 的差异

navi 的标准库默认照搬 Rust std（模块路径、类型名、trait 名、方法名和签名都相同）。
本文件列出**所有**偏离 Rust 的地方。规则见 [language.md](language.md) 1.14；每增加一处偏离，都要先在这里登记。

## 1. 统一的改写规则

| 规则 | Rust | navi | 原因 |
|---|---|---|---|
| 去掉生命周期 | `fn iter(&self) -> Iter<'_, T>` | `fn iter(&self) -> Iter<T>` | 没有生命周期 |
| 闭包参数 | `F: FnMut(T) -> U` | `fn(T) -> U` | 没有 `Fn*` trait，闭包统一为函数类型 |
| 消费 `self` 的方法 | `into_iter(self)` 会 move | 名字不变，语义变为拷贝或共享 | 没有 move |
| 宏 | `println!`、`panic!`、`assert_eq!`、`format!`、`format_args!` | 普通函数和模板字符串：模板字符串是 `fmt::Arguments`（`format_args!`），`` `...`.to_string() `` 是 `format!` | 没有宏 |

## 2. 不提供的类型和 trait

| Rust | 原因 | navi 中的写法 |
|---|---|---|
| `Box<T>` / `Box<dyn Trait>` | GC 管理内存 | `&mut T` / `&dyn Trait` |
| `Rc<T>`、`Arc<T>`、`rc::Weak`、`sync::Weak` | GC 管理内存 | `&T` / `&mut T`；弱引用是 `std::gc::Weak`（不为打破循环，为缓存：同 Go 的 `weak.Pointer`） |
| `Cell<T>`、`RefCell<T>` | 没有借用检查 | `&mut T` |
| `Cow<T>` | GC 下共享不需要拷贝 | `&T` 或 `String` |
| `Pin<T>` | GC 不移动对象 | 不需要 |
| `ManuallyDrop<T>`、`mem::drop` | 没有 Drop | 不需要 |
| `Copy`、`Send`、`Sync` | 所有值都能拷贝；不做 auto trait | 不需要 |
| `Vec::into_boxed_slice` 等和 `Box` 有关的方法 | 没有 `Box` | 无 |

## 3. 依赖 Drop 的 API

| Rust | navi | 说明 |
|---|---|---|
| channel 在所有 `Sender` 被 drop 后关闭 | `Sender::close()` | 新增 API |
| 所有 `Receiver` 被 drop 后，发送方得到 `Disconnected`（`send`、`try_send`、`send_timeout` 失败，阻塞的发送方被叫醒） | `Receiver::close()`（新增 API：丢掉通道里剩下的值）；没调用时，GC 发现没有 `Receiver` 引用了，也同样关闭，只是要等到一次回收（最迟是每 2 分钟一次的强制回收） | 没有 Drop，GC 只能事后发现 |
| `Mutex::lock()` 返回 guard，drop 时解锁 | `lock()` / `unlock()`，另有 `with_lock(\|v\| ..)`、`try_lock() -> bool`；没有锁中毒 | 共享时用 `&Mutex<T>` |
| 文件、socket、管道在 drop 时关闭 | `File::close()`、`ChildStdin`/`ChildStdout`/`ChildStderr::close()`，配合 `defer` | 重复调用无害；`Child::wait`/`wait_with_output` 会先关掉仍留在 `Child` 里的 `stdin`（同 Rust），被 `take()` 走的要自己 `close()`，子进程才读到输入结束 |

## 4. 行为差异

| 项目 | Rust | navi |
|---|---|---|
| 集合（`Vec`、`String`、`HashMap` …）的赋值 | move | 拷贝句柄，两者共享同一个容器；要独立副本用 `.clone()` |
| channel 模块 | 常用 `std::sync::mpsc`（单消费者） | `std::sync::mpmc`（`Receiver` 可以被多个线程共享） |
| `thread::spawn` | 操作系统线程 | 绿色线程（M:N 调度） |
| 定时器通道 | 没有（crossbeam-channel 的 `after`、`tick`；tokio 的 `select!` 加 `sleep`） | `std::time::after(d)`、`tick(d)`、`Ticker`（`stop()`、`reset(d)`），同 Go 1.23 的 `time.After`、`time.Tick`、`time.Ticker`，用在 `select` 里做超时和定时：运行时的定时器通道，不占线程，接收方来取时才产生值，没人引用的被 GC 回收（不必 `stop`），接收方没取走的那拍丢掉 |
| `thread::available_parallelism` | `io::Result<NonZero<usize>>` | `io::Result<usize>`（调度器同时使用的 OS 线程数：`NAVI_MAXPROCS`，否则 CPU 数） |
| —（navi 新增） | — | `thread::cpu_count() -> usize`：CPU 数，不受 `NAVI_MAXPROCS` 影响（同 Go 的 `runtime.NumCPU`） |
| `thread::current()` | `Thread` 有 name、unpark 等 | 只有 `id() -> ThreadId` |
| `ThreadId::as_u64` | 不稳定，返回 `NonZero<u64>` | 稳定，返回 `u64`（不为 0） |
| `sync::atomic` | 各种整数宽度和 `AtomicBool`/`AtomicPtr`；Ordering 按指定的语义 | 只有 `AtomicUsize`（load/store/fetch_add/compare_exchange）；所有操作都是顺序一致的 |
| 线程里 panic | 只结束该线程，`join()` 返回 `Err` | 相同 |
| panic 的载荷 | `Box<dyn Any + Send>`，通常装着消息 | `std::panic::PanicPayload`：`message()` 和 `location()`；`thread::Result<T> = Result<T, PanicPayload>` |
| `catch_unwind` 的参数 | `F: FnOnce() -> R + UnwindSafe` | `fn() -> R`（没有 `UnwindSafe`）；被捕获的 panic 仍会先打印消息（同 Rust 的默认 hook） |
| `&T` | 共享借用，保证期间没有人写 | 浅只读：只禁止通过这条路径直接写，不保证别人没在写（见 language.md 1.3） |

## 5. 暂时的差异（等对应的语言特性实现后消除）

| Rust | navi 现在 | 原因 | 何时消除 |
|---|---|---|---|

## 6. 新增的 API

| API | 说明 |
|---|---|
| `std::io::{print, println, eprint, eprintln}` | 代替 `print!` 系列宏，参数类型 `impl Display` |
| `std::panic::{panic, assert, assert_eq, assert_ne}` | 代替对应的宏，带 `#[track_caller]`；没有 `debug_assert!` 系列：只有一种构建模式（spec 1.2），断言一直生效，用 `assert`；`assert_eq<A: PartialEq<B> + Debug, B: Debug>(left: A, right: B)` 和宏一样允许两边类型不同（如 `String` 对 `&str`），参数按值传（没有 move，只是浅拷贝） |
| `Sender::close()` | 见第 3 节 |
| `std::gc` | `collect()`（同 Go 的 `runtime.GC`）、`stats()`、`set_min_heap(bytes)`（M9）、`set_percent(percent)`（同 Go 的 `debug.SetGCPercent`）、`free_os_memory()`（同 Go 的 `debug.FreeOSMemory`）、`set_memory_limit(bytes)`（同 Go 的 `debug.SetMemoryLimit`；环境变量 `NAVI_GC`、`NAVI_MEMLIMIT` 同 `GOGC`、`GOMEMLIMIT`）、`Weak<T>`（同 Go 的 `weak.Pointer`：`new(&T)`、`upgrade()`）、`add_cleanup(&T, fn(A), A) -> Cleanup`（同 Go 的 `runtime.AddCleanup`）|
| `std::unique` | `Handle<T>`（同 Go 的 `unique` 包：`Handle::new(v)`、`value()`，相等的值同一个句柄；Rust 的 std 没有） |
| `std::bench::Bencher` | `navi bench` 用（Rust 里是不稳定的 `test::Bencher`）：`iter(f)` |
| `std::serde` | 照搬 serde 这个 crate（Rust 的 std 没有）：`Serialize`、`Serializer`、`Deserialize`、`Deserializer`、`Visitor`、`SeqAccess`、`MapAccess`、`EnumAccess` 等，签名去掉生命周期；`#[derive(Serialize, Deserialize)]` 是 std 里的派生宏（`use std::serde::{Serialize, Deserialize}` 同时引入 trait 和宏，同 serde 的 derive 特性），`#[serde(...)]` 支持 rename、rename_all、skip、skip_serializing、skip_deserializing、default、skip_serializing_if、deny_unknown_fields；枚举的四种表示都有：默认的外部标记，`tag`（内部标记）、`tag` + `content`（相邻标记）、`untagged`（反序列化时先读成 `private::Content` 再按标记或逐个变体去试，同 serde） |
| `std::json` | 照 serde_json 这个 crate：`to_string`、`to_string_pretty`、`to_writer`、`from_str`、`from_reader`，类型要实现 `Serialize` / `Deserialize`（一般用派生），返回 `Result<_, json::Error>`；`json::Value` 表示任意 JSON |
| `std::any::type_name` | 和 Rust 一样，但名字不带模块路径（`Vec<i32>`，Rust 是 `alloc::vec::Vec<i32>`）；`TypeId`、`Any` 不提供 |

## 5. M7 补充标准库时新增的偏离

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| 文件关闭 | `File` drop 时关闭 | `File::close()`；`fs::read`/`fs::write` 等整文件函数自己关闭 | 没有 Drop |
| `io::BufWriter` | drop 时 flush | 必须显式 `flush()` 或 `into_inner()` | 没有 Drop |
| `Path`/`PathBuf` | 基于 `OsStr`，`Path` 是 unsized 类型 | 基于 UTF-8 字符串；`Path` 是持有 `&str` 的普通结构体；`file_name`/`extension`/`file_stem` 返回 `Option<&str>` 而不是 `Option<&OsStr>` | 没有 `OsStr`；unsized 结构体暂不支持 |
| `process::Command` | 直接 fork/exec，可配置 stdin/stdout | POSIX 用 `posix_spawn`（Windows 用 `CreateProcessW`），API 同 Rust（见第 7 节） | — |
| `Vec::drain` | 返回 `Drain<'_, T>` 惰性迭代器 | 立即移走元素，返回 `vec::IntoIter<T>` | 没有生命周期绑定的借用迭代器 |
| `hash::DefaultHasher` | SipHash-1-3（抗哈希洪水） | FxHash（快，但不抗攻击） | 编译器式负载优先速度 |
| `HashMap` 迭代顺序 | 随机种子 | 确定（取决于插入历史） | 便于编译器输出稳定 |
| `str` 的模式参数 | `Pattern` 支持 `&str`、`char`、`&[char]`、`[char; N]`、闭包 | `std::str::pattern::Pattern` 支持 `&str`、`&String`、`char`、`fn(char) -> bool`、`&[char]`、`[char; N]`、`&[char; N]` | — |
| `f32` 解析 | 精确舍入 | 先精确解析成 `f64` 再转换（极少数情况下有两次舍入） | 简化 |
| `BTreeSet::union` | `a.union(&b)` | `a.r#union(&b)` | `union` 在 navi 是保留字 |
| `BTreeMap`/`BTreeSet` 的查找参数 | `get<Q>(&self, key: &Q) where K: Borrow<Q>`；`range<T, R: RangeBounds<T>>` | `get(&self, key: &K)`；`range<R: RangeBounds<K>>`（同 `HashMap`） | 没有 `Borrow` |
| `BinaryHeap::peek_mut` | 返回 `PeekMut`，drop 时下沉 | 不提供；用 `pop` 再 `push` | 没有 Drop |
| `BinaryHeap::into_sorted_vec` | 原地排序 | 返回排好序的新 Vec，堆本身不变 | 没有 move：原地排序会破坏仍共享缓冲区的堆 |
| `BinaryHeap::drain` | 返回惰性 `Drain<'_, T>` | 立即取走，返回 `vec::IntoIter<T>` | 同 `Vec::drain` |
| `str::matches` | 返回迭代器 | 迭代器（另有 `matches_count`） | — |

## 7. 补全常用方法时新增的偏离

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| `[T]::concat` / `[T]::join` | `concat<Item>() -> <Self as Concat<Item>>::Output`，`Concat`/`Join` 按 `Borrow` 实现 | `concat<O>() -> O where [T]: Concat<Output = O>`、`join<Sep, O>(sep) -> O where [T]: Join<Sep, Output = O>`；`slice::Concat` 没有 `Item` 参数；实现了 `[S: AsRef<str>]`（得到 `String`）、`[Vec<T>]`、`[&[T]]`、`[[T; N]]`（得到 `Vec<T>`），`join` 的分隔符可以是 `&str`、`&T`、`&[T]` | 类型位置的 `<X as Trait>::Name` 会让编译器崩溃；没有 `Borrow` |
| `Iterator::cloned` / `copied` / `flatten` 的返回类型 | `Cloned<I>`、`Copied<I>`、`Flatten<I>` | `Cloned<I, T>`、`Copied<I, T>`（`T` 是被引用的类型）、`Flatten<I, U>`（`U` 是 `I::Item`） | 编译器不支持只经由关联类型约束的 impl 参数（`impl<T, I: Iterator<Item = &T>> Iterator for Cloned<I>` 会编错） |
| `Iterator::eq`、`ne`、`lt`、`le`、`gt`、`ge`、`cmp`、`partial_cmp` | 有 | 不提供 | 与 `PartialEq`/`PartialOrd`/`Ord` 的同名方法冲突：接收者是还没定类型的整数字面量时（`1.cmp(&2)`）会选中 `Iterator` 的方法 |
| `Iterator::try_fold`、`try_for_each` | 用 `Try` trait | 不提供 | 没有 `Try` trait |
| `Flatten`、`FlatMap`、`StepBy` 的 `DoubleEndedIterator` | 有 | 不提供 | 简化 |
| `[T]::get` | 参数可以是下标或区间 | 只能是下标（`str::get` 只接受区间，同 Rust） | 没有 `SliceIndex` |
| `char::to_uppercase` / `to_lowercase`、`is_uppercase` / `is_lowercase` | 完整的 Unicode 大小写映射 | 只覆盖拉丁字母（ASCII、Latin-1、扩展 A、扩展附加）、希腊字母、西里尔字母、亚美尼亚字母和全角拉丁字母（另有 `ß` → `SS`、`İ` → `i̇`）；其他字符映射为自身。`str::to_uppercase` / `to_lowercase` 按字符调用它们，词尾的 `Σ` 变成 `ς` | 没有 Unicode 表 |
| `char::is_whitespace` | Unicode White_Space | 相同 | — |
| `f32` 的数学函数 | 调用 `f32` 版本的 libm 函数 | 转成 `f64` 计算再舍入回 `f32` | 简化 |
| `RangeInclusive` 迭代 | 有 `exhausted` 标志，`0..=u8::MAX` 能迭代完 | 没有该标志：正向迭代到类型最大值时溢出（已有问题），反向迭代 `MAX..=MAX` 溢出 | 结构体由编译器构造，加字段要改编译器 |
| `String::drain` | 惰性迭代器，drop 时移走 | 立即移走文本，返回被移走字符的迭代器 `string::Drain` | 同 `Vec::drain` |

## 8. 操作系统层（std::fs / io / env / process）的偏离

std 在私有的 `std::sys` 之上实现这些模块（POSIX：文件描述符、`stat`、`opendir`、`posix_spawn`、`waitpid`；Windows：Win32 句柄和 UTF-16 的 `*W` 函数，如 `FindFirstFileW`、`CreateProcessW`；命令行从 `GetCommandLineW` 按 C 运行时的规则拆分，控制台输出用 `WriteConsoleW`，同 Rust）。

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| `OsStr`/`OsString` | 文件名、环境变量、命令参数用 `OsStr` | 一律 UTF-8 字符串（Windows 上经 UTF-16 的 `*W` 函数，未配对的代理项读成 U+FFFD）：`DirEntry::file_name`、`env::var_os`、`env::vars_os` 返回 `String`；`Command::new/arg/env`、`env::var/set_var` 的参数是 `AsRef<str>`；读到的非 UTF-8 名字按 lossy 转换 | 没有 `OsStr` |
| `io::Error::new(kind, error)` | `error` 是任意 `Into<Box<dyn Error>>` | 消息是 `AsRef<str>`（`&str` 或 `String`）；`get_ref` 返回 `Option<&str>`，`into_inner` 返回 `Option<String>`；没有 `source`、`downcast`，也没有 `std::error::Error` trait | 没有 `dyn Error` 装箱 |
| `io::ErrorKind` | `Copy`，`as_str` 私有 | 不是 `Copy`（用 `clone()`）；`as_str()` 公开（即 `Display` 的文字）；`Uncategorized` 公开（Rust 隐藏） | 没有 `Copy` |
| `fs::read_dir` | 惰性读目录，`ReadDir` drop 时关闭 | `read_dir` 调用时一次读完所有项并关闭目录，`ReadDir` 遍历这份列表 | 没有 Drop，惰性读会泄漏目录句柄 |
| `fs::File` | 不可 `Clone`，drop 时关闭 | 拷贝 `File` 共享同一个描述符，只有 `close()` 关闭；没有 `try_clone`、`lock`、`&File` 上的 `Read`/`Write` | 没有 Drop；值语义 |
| `Permissions` | Unix 上是 mode | 相同（`std::os::unix::fs::PermissionsExt`）；Windows 上只有只读属性 | — |
| `io::Stdin` | 全局 `BufReader` 加 `Mutex`，`lock()` 独占 | 全局缓冲区共享，但没有锁：`StdinLock` 不独占，多个绿色线程同时读标准输入会交错 | 简化 |
| `io::Stdout` | 行缓冲，`lock()` 独占 | 不缓冲（每次 `write` 直接写出）；`StdoutLock`/`StderrLock` 只是转发，没有锁 | 沿用原来的实现 |
| `Cursor<Vec<u8>>` | 拥有这个 Vec | Vec 是引用类型：写入会出现在创建它的那个 Vec 里 | 见第 4 节集合赋值 |
| `Seek::stream_len`、`Read::read_vectored`、`read_buf`、`IoSlice` | 有（部分不稳定） | 没有 | 暂不需要 |
| `env::set_var` / `remove_var` | Rust 2024 起是 `unsafe fn` | 相同（`unsafe fn`）；名字为空或含 `=`、NUL 时 panic | — |
| `env::vars`（Linux） | 读 libc 的 `environ` | 由初始环境（`argv` 之后的 `envp`）里的名字加上之后用 `set_var` 设过的名字，逐个 `getenv` 得到；C 代码直接加进 `environ` 的变量看不到 | navi 的链接器只导入函数，拿不到 `environ` 变量 |
| `env::home_dir` | 没有 `HOME` 时查 `getpwuid_r` | 只看 `HOME`（Windows：`USERPROFILE`） | 简化 |
| `env::split_paths` / `join_paths` | `SplitPaths` 迭代器 / `Result<OsString, _>` | `vec::IntoIter<PathBuf>` / `Result<String, JoinPathsError>` | 没有 `OsString` |
| SIGPIPE | 程序启动时忽略 | 第一次创建管道（`Stdio::piped()`）时才忽略；子进程恢复默认处理 | 运行时不处理信号 |
| `Command` 找程序 | 在子进程的 `PATH` 里找 | 相同：std 自己在子进程环境的 `PATH`（没有就用父进程的）里找，找不到返回 `NotFound`（bionic 的 `posix_spawnp` 只会让子进程以 127 退出） | — |
| `Command::get_args` / `get_envs` | `CommandArgs` / `CommandEnvs` 迭代器 | `slice::Iter<String>` / `slice::Iter<(String, Option<String>)>` | 简化 |
| `Command::output` 同时读 stdout 和 stderr | `poll`（Windows：重叠 I/O） | POSIX 用 `poll`；Windows 上 stderr 在另一个绿色线程里读 | 匿名管道不能 poll |
| `Child` | 不可 `Clone` | 拷贝共享同一个子进程和管道；`wait` 得到的状态只记在被调用的那份里 | 值语义 |
| 平台扩展 | `std::os::unix`、`std::os::windows`、`std::os::fd` | 有 `std::os::unix::fs`（`symlink`、`PermissionsExt`、`MetadataExt`、`OpenOptionsExt::mode`、`DirBuilderExt::mode`、`FileTypeExt`）、`std::os::unix::process::ExitStatusExt`、`std::os::windows::fs`（`symlink_file`、`symlink_dir`、`MetadataExt`）、`std::os::windows::process::ExitStatusExt`；没有 `std::os::fd`（`AsRawFd` 等） | 先做常用的 |

## 9. 网络（std::net）的偏离

socket 底层都是非阻塞的：会阻塞的操作只挂起当前绿色线程，由运行时的网络轮询器（macOS 用 kqueue，Linux/Android 用 epoll）唤醒，和 Go 一样。

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| 关闭 socket | drop 时关闭 | `TcpListener`、`TcpStream`、`UdpSocket` 提供 `close()`；拷贝共享同一个 socket，任何一份 `close()` 都会关闭它，正阻塞在上面的线程得到错误 | 没有 Drop |
| `try_clone` | `dup` 出新的描述符 | 返回共享同一个 socket（和它的超时设置）的拷贝 | 值语义 |
| `TcpListener::incoming` | 迭代器借用监听器 | 迭代器持有监听器的拷贝 | 没有生命周期 |
| 超时 | `SO_RCVTIMEO` / `SO_SNDTIMEO` | 由轮询器计时，超时返回 `ErrorKind::TimedOut`（Rust 在 Unix 上是 `WouldBlock`） | 非阻塞 socket 不用内核超时 |
| Windows | 阻塞 socket | 同 Go：重叠 I/O 和完成端口，会阻塞的操作只挂起绿色线程，超时用 `CancelIoEx` 取消；数据经运行时的缓冲（每次读写至多 64KB，每个 socket 的读、写各留一个复用）；`connect` 用 ConnectEx，要先 bind，临时端口不能和 TIME_WAIT 的连接共用；不设 `SO_REUSEADDR` | 绿色线程不占 OS 线程；内核不写绿色线程的栈（栈会移动） |

## 10. 照着常用 crate 做的模块

Go 的标准库里有、Rust 要靠第三方 crate 的功能，navi 放进 std，API 照最常用的 crate，规则同第 1 节（去掉生命周期、闭包是 `fn` 类型、没有宏）。下面列出和 crate 不同的地方。

### std::url（url crate）

| 项目 | url crate | navi | 原因 |
|---|---|---|---|
| 国际化域名 | 用 IDNA 转成 punycode | 非 ASCII 的主机名返回 `ParseError::IdnaError` | 没有 IDNA 表 |
| `Url::host` | `Option<Host<&str>>` | `Option<Host>`（`Host::Domain(String)`） | 没有默认类型参数 |
| `url[Position::A..Position::B]` | 用 `Position` 区间切片 | `url.slice(Position::A, Position::B)` | 不能用自定义类型的区间做 `Index` |
| `query_pairs_mut()` | 返回的 `Serializer` drop 时写回 URL | 每次 `append_pair` 等调用后立即写回 URL；`finish()` 返回查询串 | 没有 Drop |
| `query_pairs()` | 迭代 `(Cow<str>, Cow<str>)` | 迭代 `(String, String)` | 没有 `Cow` |
| `socket_addrs` | 参数是 `impl Fn() -> Option<u16>` | 参数是 `fn() -> Option<u16>` | 闭包类型 |
| `percent_encoding::AsciiSet` | `const fn add`，`&'static AsciiSet` 常量 | `add`/`remove`/`r#union`/`complement` 是普通方法，常量是值（`NON_ALPHANUMERIC`、`CONTROLS`）；`union` 是保留字，写 `r#union` | 没有 `const fn` 方法链 |
| `make_relative`、`from_file_path`、`to_file_path` | 有 | 没有 | 暂不需要 |

### std::http（类型照 http crate，客户端照 reqwest::blocking，路由照 Go 的 ServeMux）

| 项目 | 参考 | navi | 原因 |
|---|---|---|---|
| TLS | reqwest 用 native-tls 或 rustls，支持 TLS 1.2 和 1.3 | https 走 `std::tls`（TLS 1.3 和 1.2）；`ClientBuilder::add_root_certificate`、`tls_built_in_root_certs`、`danger_accept_invalid_certs`、`danger_accept_invalid_hostnames` 同 reqwest；证书类型是 `crypto::x509::Certificate`（`http::Certificate`） | TLS 1.2 在后面做 |
| `HeaderMap` | 键是 `HeaderName`，值是 `HeaderValue` | 键和值都是字符串：`get` 返回 `Option<&str>`，`get_all` 返回 `Vec<&str>`；名字不区分大小写，存成小写，按插入顺序保留多个值 | 简化 |
| `Request<T>` / `Response<T>` | 泛型 body | 不带类型参数，body 是 `Body`（字节或 `&mut dyn Read`）；收到的消息的 body 从连接上读 | 简化 |
| `Method` | `Method::GET` 等常量 | 相同；不是 `Copy`（值类型，赋值即拷贝） | 没有 `Copy` |
| 读取响应 | reqwest `text()` / `bytes()` / `json()` | 相同（按值接收）；`json::<T>()` 用 `std::json` | — |
| 构造响应（服务端） | tiny_http `Response::from_string` 等 | `Response::from_string`、`from_html`、`from_json(&x)`、`from_body`、`error(status, msg)`（同 Go 的 `http.Error`）、`redirect`；链式修改用 `with_status`、`with_header`、`with_body` | `text`/`json` 已经是读取方法 |
| 连接复用 | 响应 drop 时归还连接 | 响应体读到结尾时归还；没读完的响应占着它的连接，下一个请求开新连接 | 没有 Drop |
| 客户端超时 | reqwest `timeout` 是整个请求的时限 | 每次连接、读、写各自的时限 | 简化 |
| 服务端 | Go `http.Handler` 接口 | 处理函数是 `fn(&mut Request) -> Response`；每个连接一个绿色线程；`Router::handle` 自己也是这种函数，用闭包 `\|req\| r.handle(req)` 传给 `Server::serve` | 没有 `Fn` trait |
| 处理函数 panic | Go 的服务器恢复 panic，只断开这个连接 | 结束整个程序 | 还没有 `catch_unwind` |
| `Content-Type` | Go 用 `DetectContentType` 嗅探 | 有 body 又没设时一律 `text/plain; charset=utf-8` | 简化 |
| 未读完的请求体 | Go 读掉最多 256 KiB 后复用连接 | 相同 | — |
| 路由 | Go 1.22 `ServeMux` 模式 | 支持 `[METHOD ][HOST]/path`、`{name}`、`{name...}`、`{$}`、末尾 `/` 的子树、404/405（带 `Allow`）、子树根补斜杠的 301；不做路径清理（`//`、`..`）的重定向；冲突检查只查完全相同的模式 | 简化 |
| HTTP/2 | reqwest 和 Go 都自动协商 | 同 Go：TLS 上 ALPN 选 h2；明文要服务端 `Protocols::set_unencrypted_http2(true)`（Go 1.24 的 `http.Protocols`）、客户端 `http2_prior_knowledge()`（reqwest 的名字）；`http1_only()` 关掉；不做服务端推送、优先级、trailers（收到的丢掉）、`Upgrade: h2c`（RFC 9113 已删） | — |
| 代理、Cookie jar、压缩 | reqwest 有 | 没有 | 暂不需要 |

### std::tls（API 照 native-tls crate，实现照 Go 的 crypto/tls）

| 项目 | native-tls | navi | 原因 |
|---|---|---|---|
| 实现 | 调系统的 TLS 库（OpenSSL、Secure Transport、SChannel） | 纯 navi 实现（同 Go），证书验证用 `crypto::x509`（macOS、Windows 上是系统的验证器） | 用户选择照 Go 自己实现 |
| 版本 | 系统支持的都行 | TLS 1.3 和 1.2 的客户端和服务端（同 Go 的默认）：1.2 是 ECDHE 加 AES-GCM、ChaCha20-Poly1305 或 AES-CBC-SHA；不支持重协商，不支持 1.1 及更早 | 同 Go |
| `connect` | 返回 `Result<TlsStream<S>, HandshakeError<S>>`（可能 `WouldBlock`） | 返回 `Result<TlsStream<S>, Error>`；流都是阻塞的 | 没有非阻塞流 |
| `Certificate` | `native_tls::Certificate` | 就是 `crypto::x509::Certificate` | 不需要两个类型 |
| `Identity`、`TlsAcceptor` | `Identity::from_pkcs8`、`from_pkcs12` | `Identity::from_pkcs8`（私钥也可以是 PKCS#1、SEC 1 的 PEM）、`from_parts`；没有 PKCS#12；`TlsAcceptorBuilder::alpn_protocols` 是新增的（同 Go 的 NextProtos） | 不需要 PKCS#12 |
| 客户端证书 | `TlsConnectorBuilder::identity`；服务端没有 | 客户端同 native-tls；服务端照 Go：`TlsAcceptorBuilder::client_auth(ClientAuth::…)`（Go 的五种 ClientAuthType）和 `add_client_ca`，`TlsStream::peer_certificate` 取客户端证书 | native-tls 服务端不支持 |
| 会话恢复、0-RTT | 有（由平台决定） | 同 Go：服务端默认发 ticket（会话状态用每个 `TlsAcceptor` 随机的密钥 AES-GCM 加密，7 天有效，带客户端证书）；客户端默认不缓存，`TlsConnectorBuilder::session_cache(n)` 开启（Go 的 ClientSessionCache，按服务器名的 LRU，同一个 connector 的克隆共享）；`TlsStream::did_resume()`（Go 的 DidResume）。TLS 1.3 用 PSK 加 (EC)DHE，TLS 1.2 用 RFC 5077 的 session ticket（客户端只在开了缓存时发这个扩展）；没有 0-RTT，也没有 TLS 1.2 的会话 ID 缓存（同 Go） | 0-RTT 不做（同 Go 的默认） |
| 没有 close_notify 就断开 | 视实现而定 | 记录边界上的断开当作数据结束（同 Go） | 很多服务器这样关连接 |

### std::regex（regex crate）

语法和语义同 regex crate（RE2：不支持反向引用和环视，匹配时间与输入长度成线性）。Unicode 表来自 Go 的 unicode 包（Unicode 15.0），由 `tools/genregex` 生成。

| 项目 | regex crate | navi | 原因 |
|---|---|---|---|
| 用闭包计算替换文本 | `replace_all(text, \|caps\| ...)`（`Replacer` 对 `FnMut` 实现） | `replace_all_with(text, \|caps\| ...)`（还有 `replace_with`、`replacen_with`）；`Replacer` 只对 `&str`、`String`、`NoExpand` 实现 | 没有 `Fn` trait |
| `replace` 等的返回值 | `Cow<str>` | `String` | 没有 `Cow` |
| `&caps[i]`、`&caps["name"]` | `Index`，组不存在时 panic | `caps.at(i)`（组没参与匹配时是 `""`）；按名字用 `caps.name("n")` | 不能对返回 `str` 的 `Index` 做重载 |
| `Match::range` | `Range<usize>` | 相同 | — |
| `regex::bytes`、`Regex::captures_read`、`CaptureLocations`、`RegexSet` 的 `matches_read_at` | 有 | 没有 | 暂不需要 |
| `\p{..}` | 全部 Unicode 属性 | 一般类别、文字（script）和 `Alphabetic`、`White_Space`、`Lowercase`、`Uppercase` 等少数属性 | 表的大小 |
| 大小写折叠 | 简单折叠（simple case folding） | 相同 | — |
| 性能 | 惰性 DFA、字面量优化 | 字面量前缀和首字节跳跃，之后是 Pike VM（求分组时短匹配用有界回溯）；没有 DFA，Pike VM 路径比 Go 的 regexp 慢一个数量级（navi 还不做内联） | 先保证正确 |

### std::rand（rand crate 0.9）

同一个种子得到的值和 rand 0.9 相同（`StdRng` 是 ChaCha12，`SmallRng` 是 Xoshiro256++；`random_range`、`Uniform`、`shuffle`、`choose`、`WeightedIndex` 等的抽取方式都照 0.9），已经用 crate 核对过。0.8 的名字（`thread_rng`、`gen`、`gen_range`、`gen_bool`）保留为别名。

| 项目 | rand | navi | 原因 |
|---|---|---|---|
| `rand::rng()`（`ThreadRng`） | 每个线程一个生成器 | 整个进程共享一个 ChaCha12，加锁使用；每 64 KiB 输出从操作系统重新取种子 | 绿色线程很多，线程局部的生成器不划算 |
| `OsRng` 的来源 | `getrandom` crate | macOS `getentropy`；Linux/Android `getrandom` 系统调用（不可用时读 `/dev/urandom`）；Windows `rand_s` | — |
| `StandardUniform` 的元组 | 最多 12 个元素 | 最多 6 个 | 先做常用的 |
| `IteratorRandom::choose`、`choose_multiple`，`IndexedRandom::choose_multiple` | 有 | 有，分布相同，但抽取顺序和 crate 不同（同一个种子的结果不同） | 简化 |
| `WeightedIndex::new` | 参数是元素可 `Borrow<X>` 的迭代器 | 元素就是 `X` 的迭代器（传数组或 `Vec`，不能传 `&[X]`） | 没有 `Borrow` |
| `Distribution::map`、`sample_iter` | 闭包是 `FnMut` | `fn(T) -> U` | 闭包类型 |
| `rand::distr` 的其他分布（`Slice`、`WeightedAliasIndex`、rand_distr 里的正态分布等） | 有 | 没有 | 暂不需要 |

### std::chrono（chrono crate 0.4）

格式化、解析、RFC 3339/2822、日历和 `TimeDelta` 的结果和 chrono 0.4.44 相同（用同一个程序对比过，包括 `TZ` 设为几个不同时区时的 `Local`）。

| 项目 | chrono | navi | 原因 |
|---|---|---|---|
| `Local` | 读 tz 数据库（Unix）或 Win32 API | 用 C 库的 `localtime_r`（Windows：`_localtime64_s`），同样遵守 `TZ` | 不自带时区数据库 |
| `Local` 的 `Ambiguous` | 文档说是 `(earliest, latest)`，0.4.44 在 Unix 上实际按偏移量从小到大排（较晚的在前） | 按文档：较早的在前 | 照文档的语义 |
| `Copy` | `NaiveDate`、`DateTime` 等是 `Copy` | 值类型，赋值就是拷贝 | 没有 `Copy` |
| `LocalResult::map` | 参数是 `FnOnce` | `fn(T) -> U` | 闭包类型 |
| `format` 的返回值 | `DelayedFormat<StrftimeItems>` | `DelayedFormat`（不带类型参数）；格式串在显示时解析，不合法时 `to_string` panic（同 chrono） | 简化 |
| `StrftimeItems`、`Parsed` 的逐字段 setter、`format::Locale`、`serde` 支持、`chrono-tz` | 有 | 没有（`Parsed` 只有 `to_*` 方法） | 暂不需要 |
| `NaiveDate::from_ymd` 等会 panic 的旧接口（0.4 已弃用） | 有 | 只有 `_opt` 版本 | 照新接口 |

### std::base64、std::hex、std::crypto、std::hash::{crc32, adler32}

API 照 base64 0.22（`Engine`）、hex、RustCrypto（`Digest`、`Mac`、`Hmac<D>`）、crc32fast 和 adler32 crate。

| 项目 | crate | navi | 原因 |
|---|---|---|---|
| `Digest::finalize` | 返回定长的 `GenericArray` | 返回 `Vec<u8>` | 没有 const 泛型数组输出 |
| `Hmac<D>` | `Hmac<Sha256>` 等 | 相同；`Mac` trait 的 `finalize` 返回 `Vec<u8>`（crate 是 `CtOutput`，比较时恒定时间），比较请用 `verify_slice` 或 `crypto::subtle::constant_time_eq` | 简化 |
| 哈希器写入 | `Digest::update`、`io::Write` | 相同（都实现 `io::Write`，可以 `io::copy` 进去） | — |
| base64 的自定义 `GeneralPurpose` 配置、`read`/`write` 包装器 | 有 | 只有四个预设引擎（`STANDARD`、`STANDARD_NO_PAD`、`URL_SAFE`、`URL_SAFE_NO_PAD`） | 暂不需要 |
| crc32 的 `Hasher::new_with_initial` | 有 | 有；没有 SIMD 加速 | — |
| AEAD（`aead`、`aes`、`aes-gcm`、`chacha20poly1305`、`hkdf` crate） | 密钥、nonce、标签是定长数组（`GenericArray`），`encrypt_in_place_detached` 返回 `Tag` | `crypto::aead`、`crypto::aes`、`crypto::aes_gcm`、`crypto::chacha20poly1305`、`crypto::hkdf`，用 `&[u8]`，长度不对返回错误；`encrypt_in_place_detached` 返回 `Vec<u8>` 的标签 | 没有定长数组输出 |
| SHA-256 的实现 | sha2 crate 有 CPU 指令时用指令 | 相同：arm64 的 SHA2 指令、x86 的 SHA 扩展（`.s` 汇编，同 Go）；M4 上约 2.8 GB/s（软件约 0.16 GB/s）；`NAVI_DEBUG=noaes` 也让它走软件 | — |
| AES、AES-GCM 的实现 | 有 CPU 指令时用指令，否则软件 | 相同：arm64 的 AES/PMULL、x86 的 AES-NI/PCLMULQDQ 用 `.s` 汇编（docs/asm.md），计数器模式和 GHASH 都在汇编里；两种架构上都同 Go 一遍完成（八块一组：计数器模式八块交错过各轮，GHASH 用 H 到 H^8 合并八块、只约简一次，和下一组的 AES 交错在同一个循环里），M4 上 AES-128-GCM 约 9.4 GB/s（同 Go）；软件路径的 GHASH 是恒定时间的（BearSSL ctmul64），AES 查表；`NAVI_DEBUG=noaes` 强制走软件 | — |
| ChaCha20-Poly1305 | 有 SIMD 汇编 | 同 Go：arm64 上 ChaCha20 用 NEON 一次算四块（照 Go 的 chacha_arm64.s），amd64 上用 SSE2 一次一块；Poly1305 用 64 位分量（照 Go），两个架构上的分块循环是汇编；其他架构是纯软件。M4 上约 1.3 GB/s（Go 1.25 GB/s） | |
| X25519（`x25519-dalek`） | `StaticSecret`、`PublicKey`、`SharedSecret`、`x25519` | `crypto::x25519`，相同；恒定时间（51 位分肢，蒙哥马利阶梯） | — |
| Ed25519（`ed25519-dalek`） | `SigningKey`、`VerifyingKey`、`Signature`，`sign`、`verify`（`Signer`/`Verifier` trait） | `crypto::ed25519`：同名类型，`sign`、`verify` 是固有方法（没有 `signature` crate 的 trait）；`SignatureError` 不实现 `std::error::Error`（还没有这个 trait）；验签照 Go 的 crypto/ed25519（S 必须小于 L，接受非规范的 A、R 编码），`verify_strict` 同 `verify`；没有 Ed25519ph/ctx。M4 上签名约 38 µs、验签约 59 µs（Go 14 / 28 µs） | 先求正确，再追速度 |
| P-256、P-384（`p256`、`p384` crate） | `ecdsa::{VerifyingKey, SigningKey, Signature}`、`ecdh` | `crypto::p256`、`crypto::p384`：签名和验签（`SigningKey`、`VerifyingKey`、DER 或定长的 `Signature`，签名的 nonce 用系统随机数）和 ECDH（`ecdh::{EphemeralSecret, PublicKey, SharedSecret}`）；每条曲线由 `tools/gennistec.py` 生成专用代码（同 Go 的 nistec：定长数组的域和标量运算；点用 Go 的 P-256 汇编里的 Jacobian 公式，带符号 5 位窗口常数时间查表，基点用仿射倍数表加混合加法），P-256 的域运算和查表在 arm64、amd64 上是汇编（同 Go，`tools/genp256asm.py` 生成），M4 上 P-256 ECDH 约 44 µs、签名约 21 µs、验签约 61 µs，P-384 验签约 0.3 ms（Go 27 / 16 / 36 µs、0.33 ms）| TLS 客户端只需要验签和密钥交换 |
| RSA（`rsa` crate） | `RsaPublicKey`、`RsaPrivateKey`，`verify` / `sign`（`Pkcs1v15Sign`、`Pss`），加解密 | `crypto::rsa`：验签和签名（`RsaPrivateKey::from_pkcs1_der` / `from_pkcs8_der`，私钥运算是恒定时间的定长窗口，有 PKCS#1 的 CRT 值时用 CRT；蒙哥马利乘法照 Go 的 bigmod，核心的 z += x·y 在 arm64、amd64 上是汇编，原地计算不分配；M4 上 2048 位签名约 1.0 ms、验签约 31 µs，Go 约 0.73 ms、22 µs），没有加解密；`Pss::new_with_any_salt` 同 Go 的 `PSSSaltLengthAuto` | TLS 只需要签名和验签 |
| PEM（`pem` crate） | `parse`、`parse_many`、`encode`、`Pem` | `std::pem`，相同；不保留 RFC 1421 的头部（读的时候跳过） | 用不到 |
| X.509（Go 的 crypto/x509；Rust 没有对应的常用 crate） | `Certificate`、`CertPool`、`VerifyOptions`、`Verify`、`VerifyHostname`、`SystemCertPool` | `crypto::x509`：解析（`Certificate::from_der` / `from_pem`）和验证链，行为和错误信息与 Go 1.26 一致（40 个用例逐字对照）；字段改成方法，时间是 Unix 秒；签名支持 RSA（PKCS#1 v1.5、PSS）、ECDSA P-256/P-384 和 Ed25519（公钥、PKCS#8 私钥照 RFC 8410）；不检查证书策略；名称约束只检查 DNS 名和 IP，带邮箱或 URI 约束且证书有这类名字时验证失败；系统根证书同 Go：macOS 用 Security.framework 的 SecTrustEvaluateWithError，Windows 用 CryptoAPI 的 CertGetCertificateChain（这两个平台的吊销检查和策略由系统做），其他平台读 CA 文件，不做吊销检查。`crypto::der` 是读 DER 的游标（同 Go 的 cryptobyte） | 只做 TLS 客户端需要的部分 |

### std::compress（flate2 crate）

DEFLATE、zlib、gzip 都是 std 自己实现的（没有用 miniz 或 zlib）：编码是哈希链 LZ77（4 级以上惰性匹配）加动态 Huffman，压缩率和 zlib 相当；解码支持全部块类型。已经和 Python 的 zlib/gzip、系统的 gzip 互相验证过。

| 项目 | flate2 | navi | 原因 |
|---|---|---|---|
| 写入方向的编码器结束 | drop 时写完结尾 | 必须调用 `finish()`（返回内部的 writer）或 `try_finish()` | 没有 Drop |
| `write::*Decoder` | 边写边解压 | 写入的数据先缓存，`try_finish`/`finish` 时一次解压写出 | 解码器是"拉"输入的 |
| `bufread::*`、`Decompress`/`Compress` 底层接口、`GzHeader` 的 `mtime_as_datetime`、zlib 预设字典 | 有 | 没有 | 暂不需要 |
| 写入编码器的 `flush` | 同步刷新（sync flush） | 相同 | — |
| 性能 | miniz/zlib（C） | 约为 C 实现的十分之一到二十分之一（navi 还不内联） | — |

### std::log（log crate 加 env_logger）

| 项目 | log / env_logger | navi | 原因 |
|---|---|---|---|
| `error!`、`info!` 等 | 宏；级别关闭时参数不求值 | 函数 `log::error(msg)` 等，参数是 `impl Display`；模板字符串是惰性的 `fmt::Arguments`，级别关闭时不格式化，但 `${}` 里的表达式照常求值 | 没有宏 |
| 指定 target | `info!(target: "net", ...)` | `log::log_target("net", Level::Info, msg)` | 没有宏 |
| 默认 target | 模块路径 `module_path!()` | 由调用处的文件路径推出（`src/net/http.navi` 是 `<crate>::net::http`；`src` 之外的文件取文件名） | 没有 `module_path!` |
| `set_logger` | 参数是 `&'static dyn Log` | `&dyn Log`（GC 引用） | 没有生命周期 |
| 环境变量 | `RUST_LOG` | `NAVI_LOG`（语法相同：`info`、`net=debug,warn`） | — |
| `Target::Pipe` | `Box<dyn Write + Send>` | `&mut dyn Write` | 没有 `Box` |
| 颜色、`WriteStyle` | 有 | 接受但不输出颜色 | 简化 |
| `kv`（结构化字段）、`log::Log` 的 `&'static` 全局记录器以外的用法 | 有 | 没有 | 暂不需要 |

### std::build（navi 新增：构建信息）

| 项目 | Rust / Go | navi | 原因 |
|---|---|---|---|
| 包的元数据 | `env!("CARGO_PKG_NAME")`、`env!("CARGO_PKG_VERSION")`、`env!("CARGO_PKG_DESCRIPTION")`、`env!("CARGO_PKG_AUTHORS")`（以 `:` 连接）、`LICENSE`、`REPOSITORY`、`HOMEPAGE` | `std::build::package_name()`、`package_version()`、`package_description()`、`package_authors()`（以 `, ` 连接）、`package_license()`、`package_repository()`、`package_homepage()`，编译时就是常量，来自调用所在 crate 的 `navi.toml` | 没有宏；函数比环境变量名直接 |
| 构建时从外面传值 | Rust：`option_env!("X")` 读环境变量；Go：`-ldflags "-X main.x=..."` | `navi build --var x=...`，`std::build::var("x") -> Option<&str>`（名字是字面量） | 构建依赖什么写在命令行上，不悄悄依赖环境；源码里出现 `std::build` 的 crate 才把 `--var` 算进缓存的键 |

### std::cli（clap 4 的 builder API 和 derive）

帮助、版本和错误的文本（包括相似参数提示、`--` 提示、错误里的用法行）和 clap 4.6 逐字相同，已经用同样的程序对比过。

| 项目 | clap | navi | 原因 |
|---|---|---|---|
| derive API | `#[derive(Parser, Args, Subcommand, ValueEnum)]` | 相同（`std::cli` 里的派生宏，生成 builder 代码）：字段类型决定行为（`bool` 开关、`Option` 可省、`Vec` 多个、其余必填）、`///` 注释作帮助、`#[command(...)]`、`#[arg(...)]`、`#[value(name)]`；`#[command(version)]`、`#[command(about)]`、`#[command(author)]` 不写值时取包的（`std::build`，同 clap 从 Cargo 取），没写 `name` 时用包名；`ValueEnum` 同时生成 `FromStr` 和 `Display`（`get_one` 用 `FromStr` 取值） | — |
| `value_parser!(u16).range(..)`、`["a", "b"]` | 宏和多种 `IntoResettable` | `ValueParser::i64_range(lo, hi)`、`u64_range`、`possible(&[..])`、`i64()`、`u64()`、`f64()`、`bool()`、`string()` | 没有宏 |
| `get_one::<T>` | 返回 `Option<&T>`，值在解析时按 value_parser 转换；类型不符时 panic | 返回 `Option<T>`，取值时用 `FromStr` 转换，转换失败是 `None` | 简化（GC 语言里返回拷贝即可） |
| `get_many::<T>` | `ValuesRef<T>` | `vec::IntoIter<T>` | 同上 |
| builder 方法 | 消费 `self` | `&self`，返回修改后的拷贝 | 值语义 |
| 颜色、自动换行到终端宽度、`long_help` 与 `help` 两种格式 | 有 | 不输出颜色、不按宽度换行；`render_long_help` 同 `render_help` | 简化 |
| 负数作为值（`allow_negative_numbers`）、`ArgGroup`、`Command::mut_arg`、补全脚本 | 有 | 没有 | 暂不需要 |

### std::csv（csv crate）

读写的结果和错误信息与 csv 1.4 相同（用同一个程序对比过），下面几处除外。

| 项目 | csv crate | navi | 原因 |
|---|---|---|---|
| `Position` 的 `line`、`byte` | 由 csv-core 的状态机得出：`\r\n` 的 `\n` 算在下一条记录的开头，引号字段里的换行不计行数 | 记录第一个字节的偏移和它所在的行 | 照记录的真实位置 |
| `deserialize`、`serialize` | serde | 通过 `std::serde`，和 csv crate 相同：结构体按表头名对应字段（没有表头时按位置），元组、数组、`Vec` 按位置；`Option` 字段空字符串是 `None` | 相同 |
| `headers()` | 返回 `&StringRecord` | 返回 `StringRecord`（拷贝） | 简化 |
| 写入 | `Writer` drop 时刷新缓冲 | 必须 `flush()` 或 `into_inner()` | 没有 Drop |
| `write_record` 结束只用 `write_field` 写出的记录 | `write_record(None::<&[u8]>)` | 传一个空迭代器（如空 `Vec`） | 写法差异 |
| `StringRecord` 的 `Index`（`&rec[0]`）、`Reader::seek`、`csv::invalid_option` 等 | 有 | 用 `get(i)`；其余没有 | 暂不需要 |
