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
| `Mutex::lock()` 返回 guard，drop 时解锁 | `lock()` / `unlock()`，另有 `with_lock(\|v\| ..)` | 这一版未实现 `Mutex` |
| 文件、socket 在 drop 时关闭 | `close()`，配合 `defer` | 这一版未实现 |

## 4. 行为差异

| 项目 | Rust | navi |
|---|---|---|
| 集合（`Vec`、`String`、`HashMap` …）的赋值 | move | 拷贝句柄，两者共享同一个容器；要独立副本用 `.clone()` |
| channel 模块 | 常用 `std::sync::mpsc`（单消费者） | `std::sync::mpmc`（`Receiver` 可以被多个线程共享） |
| `thread::spawn` | 操作系统线程 | 绿色线程（M:N 调度） |
| 线程里 panic | 只结束该线程，`join()` 返回 `Err` | 相同（M9 起）。只有 native 后端；stage0 的解释器不捕获 panic，仍结束整个进程 |
| panic 的载荷 | `Box<dyn Any + Send>`，通常装着消息 | `std::panic::PanicPayload`：`message()` 和 `location()`；`thread::Result<T> = Result<T, PanicPayload>` |
| `catch_unwind` 的参数 | `F: FnOnce() -> R + UnwindSafe` | `fn() -> R`（没有 `UnwindSafe`）；被捕获的 panic 仍会先打印消息（同 Rust 的默认 hook） |
| `&T` | 共享借用，保证期间没有人写 | 浅只读：只禁止通过这条路径直接写，不保证别人没在写（见 language.md 1.3） |

## 5. 暂时的差异（等对应的语言特性实现后消除）

| Rust | navi 现在 | 原因 | 何时消除 |
|---|---|---|---|
| `impl From<[T; N]> for Vec<T>` | `impl From<&[T]> for Vec<T>` | const 泛型推到自举之后 | 支持 const 泛型后 |

## 6. 新增的 API

| API | 说明 |
|---|---|
| `std::io::{print, println, eprint, eprintln}` | 代替 `print!` 系列宏，参数类型 `impl Display` |
| `std::panic::{panic, assert, assert_eq, assert_ne}` | 代替对应的宏，带 `#[track_caller]` |
| `Sender::close()` | 见第 3 节 |
| `std::gc` | `collect()`、`stats()`、`set_min_heap(bytes)`（M9）；`Weak<T>`、finalizer 以后再做 |

## 5. M7 补充标准库时新增的偏离

| 项目 | Rust | navi | 原因 |
|---|---|---|---|
| 文件关闭 | `File` drop 时关闭 | `File::close()`；`fs::read`/`fs::write` 等整文件函数自己关闭 | 没有 Drop |
| `io::BufWriter` | drop 时 flush | 必须显式 `flush()` 或 `into_inner()` | 没有 Drop |
| `Path`/`PathBuf` | 基于 `OsStr`，`Path` 是 unsized 类型 | 基于 UTF-8 字符串；`Path` 是持有 `&str` 的普通结构体；`file_name`/`extension`/`file_stem` 返回 `Option<&str>` 而不是 `Option<&OsStr>` | 没有 `OsStr`；unsized 结构体暂不支持 |
| `process::Command` | 直接 fork/exec，可配置 stdin/stdout | 通过系统 shell（`/bin/sh -c`、`cmd /C`）执行，参数自动加引号；只有 `status()` | 先满足自举（调用链接器）；完整实现留到以后 |
| `Vec::drain` | 返回 `Drain<'_, T>` 惰性迭代器 | 立即移走元素，返回 `vec::IntoIter<T>` | 没有生命周期绑定的借用迭代器 |
| `hash::DefaultHasher` | SipHash-1-3（抗哈希洪水） | FxHash（快，但不抗攻击） | 编译器式负载优先速度 |
| `HashMap` 迭代顺序 | 随机种子 | 确定（取决于插入历史） | 便于编译器输出稳定 |
| `str` 的模式参数 | `Pattern` 支持 `&str`、`char`、`&[char]`、闭包 | `std::str::pattern::Pattern` 支持 `&str`、`&String`、`char`、`fn(char) -> bool` | 未实现 `&[char]` |
| `f32` 解析 | 精确舍入 | 先精确解析成 `f64` 再转换（极少数情况下有两次舍入） | 简化 |
| `str::matches` | 返回迭代器 | 只提供 `matches_count` | 简化 |
