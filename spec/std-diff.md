# navi std 与 Rust std 的差异

navi 的标准库默认照搬 Rust std（模块路径、类型名、trait 名、方法名和签名都相同）。
本文件列出**所有**偏离 Rust 的地方。规则见 [language.md](language.md) 1.14；每增加一处偏离，都要先在这里登记。

## 1. 统一的改写规则

| 规则 | Rust | navi | 原因 |
|---|---|---|---|
| 去掉生命周期 | `fn iter(&self) -> Iter<'_, T>` | `fn iter(&self) -> Iter<T>` | 没有生命周期 |
| 闭包参数 | `F: FnMut(T) -> U` | `fn(T) -> U` | 没有 `Fn*` trait，闭包统一为函数类型 |
| 消费 `self` 的方法 | `into_iter(self)` 会 move | 名字不变，语义变为拷贝或共享 | 没有 move |
| 宏 | `println!`、`panic!`、`assert_eq!`、`format!` | 普通函数和模板字符串 | 没有宏 |

## 2. 不提供的类型和 trait

| Rust | 原因 | navi 中的写法 |
|---|---|---|
| `Box<T>` / `Box<dyn Trait>` | GC 管理内存 | `&mut T` / `&dyn Trait` |
| `Rc<T>`、`Arc<T>`、`rc::Weak`、`sync::Weak` | GC 管理内存 | `&T` / `&mut T`；弱引用以后放在 `std::gc::Weak` |
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
| `Mutex::lock()` 返回 guard，drop 时解锁 | `lock()` / `unlock()`，另有 `with_lock(\|v\| ..)`、`try_lock() -> bool`；没有锁中毒 | 共享时用 `&Mutex<T>` |
| 文件、socket、管道在 drop 时关闭 | `File::close()`、`ChildStdin`/`ChildStdout`/`ChildStderr::close()`，配合 `defer` | 重复调用无害；`Child::wait`/`wait_with_output` 会先关掉仍留在 `Child` 里的 `stdin`（同 Rust），被 `take()` 走的要自己 `close()`，子进程才读到输入结束 |

## 4. 行为差异

| 项目 | Rust | navi |
|---|---|---|
| 集合（`Vec`、`String`、`HashMap` …）的赋值 | move | 拷贝句柄，两者共享同一个容器；要独立副本用 `.clone()` |
| channel 模块 | 常用 `std::sync::mpsc`（单消费者） | `std::sync::mpmc`（`Receiver` 可以被多个线程共享） |
| `thread::spawn` | 操作系统线程 | 绿色线程（M:N 调度） |
| `thread::available_parallelism` | `io::Result<NonZero<usize>>` | `io::Result<usize>`（调度器同时使用的 OS 线程数：`NAVI_MAXPROCS`，否则 CPU 数） |
| `thread::current()` | `Thread` 有 name、unpark 等 | 只有 `id() -> ThreadId` |
| `sync::atomic` | 各种整数宽度和 `AtomicBool`/`AtomicPtr`；Ordering 按指定的语义 | 只有 `AtomicUsize`（load/store/fetch_add/compare_exchange）；所有操作都是顺序一致的 |
| 线程里 panic | 只结束该线程，`join()` 返回 `Err` | 相同（M9 起）。只有 native 后端；stage0 的解释器不捕获 panic，仍结束整个进程 |
| panic 的载荷 | `Box<dyn Any + Send>`，通常装着消息 | `std::panic::PanicPayload`：`message()` 和 `location()`；`thread::Result<T> = Result<T, PanicPayload>` |
| `Duration::as_nanos` / `as_micros` / `as_millis` | `u128` | 相同；只有用 stage0 编译 std 时是 `u64`（stage0 没有 128 位整数） |
| `catch_unwind` 的参数 | `F: FnOnce() -> R + UnwindSafe` | `fn() -> R`（没有 `UnwindSafe`）；被捕获的 panic 仍会先打印消息（同 Rust 的默认 hook） |
| `&T` | 共享借用，保证期间没有人写 | 浅只读：只禁止通过这条路径直接写，不保证别人没在写（见 language.md 1.3） |

## 5. 暂时的差异（等对应的语言特性实现后消除）

| Rust | navi 现在 | 原因 | 何时消除 |
|---|---|---|---|
| `impl From<[T; N]> for Vec<T>` | navic：两者都有；stage0：只有 `impl From<&[T]> for Vec<T>` | stage0 没有 const 泛型 | — |

## 6. 新增的 API

| API | 说明 |
|---|---|
| `std::io::{print, println, eprint, eprintln}` | 代替 `print!` 系列宏，参数类型 `impl Display` |
| `std::panic::{panic, assert, assert_eq, assert_ne}` | 代替对应的宏，带 `#[track_caller]` |
| `Sender::close()` | 见第 3 节 |
| `std::gc` | `collect()`、`stats()`、`set_min_heap(bytes)`（M9）、`set_percent(percent)`（同 Go 的 `debug.SetGCPercent`）；`Weak<T>`、finalizer 以后再做 |
| `std::bench::Bencher` | `navi bench` 用（Rust 里是不稳定的 `test::Bencher`）：`iter(f)` |
| `std::reflect` | 运行时反射，照 Go 的 reflect，不需要标注（navi 没有过程宏）：`type_of::<T>()`、`value_of(&x)`、`value_of_mut(&mut x)`、`Value::new(ty)`；`Type` 能查 kind、名字、大小、字段、变体、元素类型、map 的键和值类型；`Value` 能读写基本类型和字符串，按下标或字段名访问，`push` 到 Vec，切换枚举变体，读写 HashMap。编译器只为程序里实际反射的类型（连同它们各部分的类型）生成描述，不用反射的程序不受影响 |
| `std::json` | 用反射实现的 JSON，任意类型都能用（Go 的 encoding/json；Rust 里这是 serde_json 这个 crate）：`to_string`、`to_string_pretty`、`from_str`，返回 `Result<_, json::Error>`；结构体、枚举、Option、HashMap 的对应规则和 serde_json 的默认规则相同 |
| `std::any::type_name` | 和 Rust 一样，但名字不带模块路径（`Vec<i32>`，Rust 是 `alloc::vec::Vec<i32>`）；`TypeId`、`Any` 不提供 |

## 5. M7 补充标准库时新增的偏离

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| 文件关闭 | `File` drop 时关闭 | `File::close()`；`fs::read`/`fs::write` 等整文件函数自己关闭 | 没有 Drop |
| `io::BufWriter` | drop 时 flush | 必须显式 `flush()` 或 `into_inner()` | 没有 Drop |
| `Path`/`PathBuf` | 基于 `OsStr`，`Path` 是 unsized 类型 | 基于 UTF-8 字符串；`Path` 是持有 `&str` 的普通结构体；`file_name`/`extension`/`file_stem` 返回 `Option<&str>` 而不是 `Option<&OsStr>` | 没有 `OsStr`；unsized 结构体暂不支持 |
| `process::Command` | 直接 fork/exec，可配置 stdin/stdout | navic：POSIX 用 `posix_spawn`（Windows 用 `CreateProcessA`），API 同 Rust（见第 7 节）；只有 stage0 编译的 std 仍通过系统 shell 执行、只有 `status()` | stage0 冻结 |
| `Vec::drain` | 返回 `Drain<'_, T>` 惰性迭代器 | 立即移走元素，返回 `vec::IntoIter<T>` | 没有生命周期绑定的借用迭代器 |
| `hash::DefaultHasher` | SipHash-1-3（抗哈希洪水） | FxHash（快，但不抗攻击） | 编译器式负载优先速度 |
| `HashMap` 迭代顺序 | 随机种子 | 确定（取决于插入历史） | 便于编译器输出稳定 |
| `str` 的模式参数 | `Pattern` 支持 `&str`、`char`、`&[char]`、闭包 | `std::str::pattern::Pattern` 支持 `&str`、`&String`、`char`、`fn(char) -> bool` | 未实现 `&[char]` |
| `f32` 解析 | 精确舍入 | 先精确解析成 `f64` 再转换（极少数情况下有两次舍入） | 简化 |
| `str::matches` | 返回迭代器 | 只提供 `matches_count` | 简化 |

## 7. 操作系统层（std::fs / io / env / process）的偏离

navic 编译的 std 在私有的 `std::sys` 之上实现这些模块（POSIX：文件描述符、`stat`、`opendir`、`posix_spawn`、`waitpid`；Windows：Win32 句柄、`FindFirstFileA`、`CreateProcessA`）。stage0 编译的 std 保留原来基于 stdio 和 shell 的版本（只有自举用到的那部分）。

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| `OsStr`/`OsString` | 文件名、环境变量、命令参数用 `OsStr` | 一律 UTF-8 字符串：`DirEntry::file_name`、`env::var_os`、`env::vars_os` 返回 `String`；`Command::new/arg/env`、`env::var/set_var` 的参数是 `AsRef<str>`；读到的非 UTF-8 名字按 lossy 转换 | 没有 `OsStr` |
| `io::Error::new(kind, error)` | `error` 是任意 `Into<Box<dyn Error>>` | 消息是 `AsRef<str>`（`&str` 或 `String`）；`get_ref` 返回 `Option<&str>`，`into_inner` 返回 `Option<String>`；没有 `source`、`downcast`，也没有 `std::error::Error` trait | 没有 `dyn Error` 装箱 |
| `io::ErrorKind` | `Copy`，`as_str` 私有 | 不是 `Copy`（用 `clone()`）；`as_str()` 公开（即 `Display` 的文字）；`Uncategorized` 公开（Rust 隐藏） | 没有 `Copy` |
| `fs::read_dir` | 惰性读目录，`ReadDir` drop 时关闭 | `read_dir` 调用时一次读完所有项并关闭目录，`ReadDir` 遍历这份列表 | 没有 Drop，惰性读会泄漏目录句柄 |
| `fs::File` | 不可 `Clone`，drop 时关闭 | 拷贝 `File` 共享同一个描述符，只有 `close()` 关闭；没有 `try_clone`、`lock`、`&File` 上的 `Read`/`Write` | 没有 Drop；值语义 |
| `Metadata::created` | Linux 上用 `statx` | Linux 上返回 `Unsupported` 错误（macOS、Windows 有） | 只用了 `newfstatat` 系统调用 |
| `Permissions` | Unix 上是 mode | 相同（`std::os::unix::fs::PermissionsExt`）；Windows 上只有只读属性 | — |
| `io::Stdin` | 全局 `BufReader` 加 `Mutex`，`lock()` 独占 | 全局缓冲区共享，但没有锁：`StdinLock` 不独占，多个绿色线程同时读标准输入会交错 | 简化 |
| `io::Stdout` | 行缓冲，`lock()` 独占 | 不缓冲（每次 `write` 直接写出）；`StdoutLock`/`StderrLock` 只是转发，没有锁 | 沿用原来的实现 |
| `Cursor<Vec<u8>>` | 拥有这个 Vec | Vec 是引用类型：写入会出现在创建它的那个 Vec 里 | 见第 4 节集合赋值 |
| `Seek::stream_len`、`Read::read_vectored`、`read_buf`、`IoSlice` | 有（部分不稳定） | 没有 | 暂不需要 |
| `env::set_var` / `remove_var` | Rust 2024 起是 `unsafe fn` | 相同（`unsafe fn`）；名字为空或含 `=`、NUL 时 panic | — |
| `env::vars`（Linux） | 读 libc 的 `environ` | 由初始环境（`argv` 之后的 `envp`）里的名字加上之后用 `set_var` 设过的名字，逐个 `getenv` 得到；C 代码直接加进 `environ` 的变量看不到 | navic 的链接器只导入函数，拿不到 `environ` 变量 |
| `env::home_dir` | 没有 `HOME` 时查 `getpwuid_r` | 只看 `HOME`（Windows：`USERPROFILE`） | 简化 |
| `env::split_paths` / `join_paths` | `SplitPaths` 迭代器 / `Result<OsString, _>` | `vec::IntoIter<PathBuf>` / `Result<String, JoinPathsError>` | 没有 `OsString` |
| SIGPIPE | 程序启动时忽略 | 第一次创建管道（`Stdio::piped()`）时才忽略；子进程恢复默认处理 | 运行时不处理信号 |
| `Command` 找程序 | 在子进程的 `PATH` 里找 | 相同：std 自己在子进程环境的 `PATH`（没有就用父进程的）里找，找不到返回 `NotFound`（bionic 的 `posix_spawnp` 只会让子进程以 127 退出） | — |
| `Command::get_args` / `get_envs` | `CommandArgs` / `CommandEnvs` 迭代器 | `slice::Iter<String>` / `slice::Iter<(String, Option<String>)>` | 简化 |
| `Command::output` 同时读 stdout 和 stderr | `poll`（Windows：重叠 I/O） | POSIX 用 `poll`；Windows 上 stderr 在另一个绿色线程里读 | 匿名管道不能 poll |
| `Child` | 不可 `Clone` | 拷贝共享同一个子进程和管道；`wait` 得到的状态只记在被调用的那份里 | 值语义 |
| `fs::read_link`（Windows） | 读重解析点里的目标 | 返回最终目标（`GetFinalPathNameByHandleA`） | 简化；Windows 部分无法在这里运行测试 |
| 平台扩展 | `std::os::unix`、`std::os::windows`、`std::os::fd` | 有 `std::os::unix::fs`（`symlink`、`PermissionsExt`、`MetadataExt`、`OpenOptionsExt::mode`、`DirBuilderExt::mode`、`FileTypeExt`）、`std::os::unix::process::ExitStatusExt`、`std::os::windows::fs`（`symlink_file`、`symlink_dir`、`MetadataExt`）、`std::os::windows::process::ExitStatusExt`；没有 `std::os::fd`（`AsRawFd` 等） | 先做常用的 |
