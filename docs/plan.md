# Navi 编程语言实施计划

## Context

目标是从零开发一门名为 **navi** 的语言：
- 语法高度类似 Rust（fn / struct / enum / impl / trait / match / let mut / `?` / crate + mod）
- 有 GC，**无借用检查、无生命周期、无 move 语义**
- 泛型采用 Rust 式 trait 约束，**不做 comptime**（只保留 `const fn` 常量求值）
- **不要宏语法**；字符串插值采用 JS 模板字符串的写法 `` `x = ${x}` ``
- 编译速度对标 Go；AOT 编译为原生二进制
- 并发模型：类 Go 绿色线程（M:N 调度 + channel）
- 尽早自举：原型编译器用 **Go** 写（stage0），之后用 navi 重写编译器
- 首批目标：windows-x64、macos-x64、macos-arm64、linux-x64、linux-arm64

已确定的决策：
- 原型用 Go
- **自研后端**：自研 SSA IR，直接生成机器码和目标文件，初期借用系统链接器
- 纯 GC
- 类 Go 绿色线程
- **泛型只做 trait 约束，代码生成只做 GC shape 共享 + 字典**（Go 的做法）；自举前支持关联类型、blanket impl、`impl Trait`
- **集合（Vec / String / HashMap …）是引用语义**
- **标准库向 Rust 的 `std` 看齐**：没有单独的 `core`，模块路径、类型名、trait 名、方法名和签名都照着 Rust std 定，只在 GC 语言的实际情况要求时调整（规则见 1.14）
- **标准库这一版只实现语言必需的子集**，其余部分到自举之前再单独做计划，届时同样遵守 1.14 的规则
- **并发对齐 Rust std**：`std::thread::spawn` 启动绿色线程；channel 用 `std::sync::mpmc` 的 API，并加上显式的 `close()`；保留 `select` 关键字
- **保留 `&T` 和 `&mut T` 两种引用**：struct 和 enum 是值类型，修改调用方的值、共享对象、递归数据结构都要靠引用。之所以区分两种，是为了和 Rust 的写法以及 std 的 API（`iter` / `iter_mut` 等）保持一致；`&T` 只表示"不能通过它写"，不保证别人没在写
- **`&T` 是浅只读**：只禁止通过这条路径直接写，规则见 1.3
- **runtime 是 std 的私有模块 `std::rt`**，不单独做 crate，避免 std 和 runtime 循环依赖
- **保留 `Deref`** 和解引用强制转换（`&String` → `&str`，`&Vec<T>` → `&[T]`）
- **闭包统一为 `fn(A) -> B` 类型**，没有 `Fn*` trait
- **没有宏**：打印、断言、panic 都是普通函数，格式化用模板字符串
- **Rust 式 crate + mod 树**

不做 Zig 式 comptime 的理由：comptime 泛型要到实例化时才检查，和 Go 级别的编译速度、增量编译、LSP 支持都有冲突，也和 Rust 式 trait 语法形成两套泛型规则。

当前状况：`/Users/sunli/work/navi2` 是空目录，也不是 git 仓库。宿主机是 macOS arm64，Go 1.26，有系统 clang/ld，所以**第一个后端目标是 darwin-arm64**，开发时可以随写随测。

---

## 1. 语法规范（M0 写成 `spec/`，M1 的 parser 和 sema 以此为准）

总原则：**写法尽量和 Rust 一致**，Rust 程序员不用查文档就能读懂；**但要符合 GC 语言的实际情况**，Rust 里只因为没有 GC 才存在的东西不照搬。和 Rust 不同的地方有四类：
- 删掉为所有权和借用服务的部分
- 加入 GC 和并发需要的部分
- 用模板字符串和普通函数代替宏
- 为了编译速度做的简化

### 1.1 词法

- **源文件**：UTF-8，扩展名 `.navi`
- **注释**：`//`、`/* */`（可嵌套）；文档注释 `///`（写在条目前面）、`//!`（写在 crate 或模块开头）
- **标识符**：`[A-Za-z_][A-Za-z0-9_]*`，暂不支持 Unicode 标识符；原始标识符 `r#match`
- **关键字**：
  `as break const continue crate defer dyn else enum extern false fn for if impl in let loop match mod mut pub return select self Self static struct super trait true type unsafe use where while`
- **保留字**（现在不用，留给以后）：
  `async await box macro move try union yield`
- **整数字面量**：`42`、`1_000_000`、`0xff`、`0o17`、`0b1010`；带后缀的写法如 `10u8`、`3i64`、`7usize`
- **浮点字面量**：`1.5`、`2e10`、`1.0e-3f32`
- **字符**：`'a'`、`'\n'`、`'\u{1F600}'`，类型是 `char`（Unicode 标量值）
- **字节**：`b'a'`，类型是 `u8`
- **普通字符串**：`"hello\n"`，**不做插值**，类型是 `&str`
  - 原始字符串：`r"C:\path"`、`r#"含 "引号""#`
  - 字节串：`b"abc"`，类型是 `&[u8]`
- **模板字符串**：`` `hello ${name}` ``，详见 1.13
- **转义**：`\n \r \t \\ \0 \' \" \x7f \u{...}`；模板字符串里另外有 `` \` `` 和 `\${`
- **标签**：`'outer`（只用于循环标签；navi 没有生命周期）

### 1.2 基本类型

| 类型 | 说明 |
|---|---|
| `i8 i16 i32 i64 isize` / `u8 u16 u32 u64 usize` | 整数；`i128` / `u128` 推到自举之后 |
| `f32 f64` | 浮点 |
| `bool` | `true` / `false` |
| `char` | 4 字节的 Unicode 标量值 |
| `()` | 单元类型 |
| `!` | never 类型（`panic(..)`、`loop {}`、`return` 的类型） |

- 无后缀的整数字面量默认推断为 `i32`，浮点默认为 `f64`，与 Rust 相同
- **整数溢出**：debug 构建时 panic，`--release` 时回绕（与 Rust 相同）；另外提供 `wrapping_*`、`checked_*`、`saturating_*` 方法
- **类型转换**只能用 `as`，没有隐式的数值转换

### 1.3 复合类型与引用（和 Rust 差别最大的部分）

```rust
let t: (i32, &str) = (1, "a");      // 元组
let a: [u8; 4] = [0; 4];            // 定长数组，值类型
let s: &[u8] = &a[1..3];            // 切片：GC 管理的 (ptr, len)
let p: &Point = &Point { x: 1.0, y: 2.0 };   // 引用：GC 指针
let q: &mut Point = &mut pt;        // 可写引用
let r: *const u8 = s.as_ptr();      // 原始指针，只能在 unsafe 中解引用
let f: fn(i32) -> i32 = |x| x + 1;  // 函数类型（统一表示函数和闭包，内部是 (代码指针, 环境指针)）
let cb: extern "C" fn(i32) -> i32 = callback;   // C 函数指针：只能指向 extern "C" fn，不能是闭包
let d: &dyn Display = &pt;          // trait 对象：(数据指针, vtable)
```

- **所有类型都按值拷贝**（浅拷贝，和 Go 一样）。没有 move，赋值以后原变量仍然可以用；没有 `Copy` trait，需要深拷贝时调用 `.clone()`
- **`&T` / `&mut T` 都是 GC 指针**：
  - 可以随意存放、返回、跨线程传递，没有生命周期
  - 对局部变量取 `&` 时，由逃逸分析决定变量放在栈上还是堆上（和 Go 一样）
  - **`&T` 是浅只读**，只禁止"通过这条路径直接写"：
    - 不能给 `r.field` 赋值，不能通过 `r` 调用 `&mut self` 方法，不能对 `r.field` 取 `&mut`
    - 通过 `&T` 读到的 `&mut U` 字段，类型会降级成 `&U`
    - **但拷贝出来的值不受限制**：比如 `let mut c = r.items;`（`items: Vec<i32>`）以后，`c.push(1)` 会改到原来的容器，因为集合是引用语义。spec 和文档里要写明这一点
  - **不保证别人没有在写**：允许 `&T` 和 `&mut T` 同时指向同一个对象，不做别名检查
- **在堆上分配**：直接写 `&Node { .. }` 或 `&mut Node { .. }`（相当于 Go 的 `&T{}`）。没有 `Box`、`Rc`、`Arc`、`Cell`、`RefCell`、`Pin`，它们在 Rust 里只是为了在没有 GC 的情况下管理所有权。从 Rust 迁移时这样改：
  - `Box<T>` / `Box<dyn Trait>` → `&mut T` / `&dyn Trait`
  - `Rc<RefCell<T>>` → `&mut T`
  - `Arc<Mutex<T>>` → `&Mutex<T>`
- **可空**：没有 null。`Option<&T>` 的 `None` 在内存里就是空指针，不额外占空间
- **字符串**：
  - `&str`：不可变的 UTF-8 切片，可以随意保存
  - `String`：可增长的字符串
  - 普通字符串字面量的类型是 `&str`；模板字符串默认是 `String`，期望类型是 `impl Display` 时是 `fmt::Arguments`（见 1.13）
- **集合**：`Vec<T>`、`String`（以及以后的 `HashMap`、`HashSet`、`VecDeque`、`BTreeMap`）内部是指向堆对象的引用，所以**拷贝以后共享同一个容器**（类似 Go 的 map、Java 的 ArrayList），要独立副本时用 `.clone()`。这样避免了 Go 切片那种"共享底层数组、长度却各自独立"的陷阱
- 不支持 `union`；FFI 用 `#[repr(C)] struct`

### 1.4 条目（item）

```rust
// 函数
pub fn add(a: i32, b: i32) -> i32 { a + b }
fn main() { ... }
fn main() -> Result<(), Error> { ... }      // main 也可以返回 Result

// 常量与全局变量
const MAX: usize = 1024;
const TABLE: [u32; 4] = make_table();       // 调用 const fn，在编译期求值
static mut COUNTER: i64 = 0;                // 可变全局变量，不需要 unsafe（数据竞争由用户负责，同 Go）

// 结构体
struct Point { x: f64, y: f64 }
struct Meters(f64);                         // 元组结构体
struct Unit;                                // 单元结构体

// 枚举（代数数据类型）
enum Shape {
    Circle { r: f64 },
    Rect(f64, f64),
    Empty,
}
enum Color { Red = 1, Green = 2 }           // 显式判别值

// 类型别名
type Result<T> = std::result::Result<T, Error>;

// 模块（见 1.10）
mod parser;                                 // 加载 parser.navi 或 parser/mod.navi
mod util { pub fn clamp(x: i32) -> i32 { ... } }   // 内联模块

// 外部函数（FFI）
#[link(name = "c")]
extern "C" {
    fn write(fd: i32, buf: *const u8, n: usize) -> isize;
}
#[export_name = "navi_cb"]
extern "C" fn callback(x: i32) -> i32 { x }
```

- **可见性**：同 Rust，`pub`、`pub(crate)`、`pub(super)`；不写就是对当前模块及其子模块可见
- `const fn`：只允许纯计算，不能分配 GC 对象、不能做 IO；求值有步数上限

### 1.5 impl 与 trait

```rust
impl Point {
    pub fn new(x: f64, y: f64) -> Self { Point { x, y } }   // 关联函数
    pub fn len(&self) -> f64 { (self.x * self.x + self.y * self.y).sqrt() }
    pub fn scale(&mut self, k: f64) { self.x *= k; self.y *= k; }
    pub fn into_tuple(self) -> (f64, f64) { (self.x, self.y) }  // 按值接收 = 拷贝
}

pub trait Figure: Debug {                   // 超 trait
    type Unit;                              // 关联类型
    const SIDES: u32;                       // 关联常量
    fn area(&self) -> f64;
    fn describe(&self) -> String { `area ${self.area()}` }   // 默认方法
}

impl Figure for Circle {
    type Unit = Meters;
    const SIDES: u32 = 0;
    fn area(&self) -> f64 { 3.14159 * self.r * self.r }
}

impl<T: Display> ToString for T {           // blanket impl
    fn to_string(&self) -> String { `${self}` }
}
```

- 方法调用会自动取引用和自动解引用（`p.len()` 对 `Point`、`&Point`、`&mut Point` 都能用）；调用 `&mut self` 方法时，接收者必须是 `let mut` 绑定或者 `&mut` 引用
- 固有 impl（`impl Point`）只能写在定义这个类型的 crate 里
- trait impl 遵守孤儿规则：trait 或类型至少有一个是本 crate 定义的（同 Rust）
- **impl 不允许重叠**；不支持特化、负 impl、auto trait
- **trait 对象**：`&dyn Trait`。navi 没有 `Sized`，所以规则和 Rust 稍有不同：任何 trait 都能做成 `dyn`，但只有"可经 dyn 调用"的方法进入 vtable——接收者是 `&self` / `&mut self`、没有自己的类型参数、参数和返回值里不出现 `Self`。其余方法（如 `Iterator::map`）不能通过 `dyn` 调用，效果等同 Rust 给它们加了 `where Self: Sized`。`dyn` 类型必须写出 trait 的全部关联类型（`dyn Iterator<Item = u8>`）
- **没有 `Drop`**，资源释放用 `defer`（见 1.8）

### 1.6 泛型

```rust
fn largest<T: PartialOrd>(xs: &[T]) -> &T { ... }
fn print_all<I>(it: I) where I: Iterator, I::Item: Display { ... }
struct Stack<T> { items: Vec<T> }
impl<T: Clone> Stack<T> { fn peek(&self) -> Option<T> { ... } }
enum Tree<K: Ord, V> { Leaf, Node(&Tree<K, V>, K, V, &Tree<K, V>) }

fn evens(v: &[i32]) -> impl Iterator<Item = i32> { ... }   // 返回位置的 impl Trait
fn sum(it: impl Iterator<Item = i32>) -> i32 { ... }       // 参数位置的 impl Trait

let v = parse::<i64>("42");                 // turbofish
let n = <T as Figure>::SIDES;               // 完全限定路径
```

- 泛型函数体**在定义处只检查一次**，只能使用约束里 trait 提供的方法和运算符；报错位置就在写错的地方
- 自举前支持：trait 约束、`where`、`+` 组合约束、默认方法、关联类型和关联常量、blanket impl、`impl Trait`、带自己类型参数的方法（如 `Iterator::map<B>`）、默认类型参数（`trait PartialEq<Rhs = Self>`，有默认值的参数必须排在最后，同 Rust）
- 自举后再做：const 泛型（`struct Matrix<const N: usize>`）
- 不做：GAT、高阶 trait bound（`for<'a>`）、特化
- **闭包没有专门的 `Fn` / `FnMut` / `FnOnce` trait**，统一用函数类型 `fn(A) -> B`。例如 `fn map<B>(self, f: fn(Self::Item) -> B) -> Map<Self, B>`。这样泛型和 shape 共享都简单得多

### 1.7 表达式

所有块都是表达式，最后一个不带分号的表达式就是块的值。

```rust
// 字面量与构造
Point { x: 1.0, y }                         // 字段简写
Point { x: 0.0, ..origin }                  // 结构体更新语法
Shape::Rect(1.0, 2.0)
[1, 2, 3]   [0u8; 256]   (a, b)   ()
`(${p.x}, ${p.y})`                          // 模板字符串，默认类型 String（见 1.13）

// 运算符（优先级和结合性同 Rust）
-x  !b  *p  &x  &mut x
a * b / c % d    a + b - c    a << 2 >> 1    a & b ^ c | d
a == b  a != b  a < b  a <= b  a > b  a >= b
a && b  a || b
x as u64
a..b  a..=b  ..b  a..  ..                   // 区间
x = y   x += 1   x -= 1   x *= 2   x /= 2   x %= 3   x &= m   x |= m   x ^= m   x <<= 1   x >>= 1
f()?                                        // 错误传播

// 访问
p.x   t.0   v[i]   s[1..3]   obj.method(a)   Type::assoc(a)   crate::path::func()

// 控制流（都是表达式）
let y = if x > 0 { 1 } else { -1 };
let z = match shape { Shape::Circle { r } => r, _ => 0.0 };
let w = loop { if done() { break 42; } };
'outer: for i in 0..10 { for j in 0..10 { if j == i { continue 'outer; } } }
while n > 0 { n -= 1; }
for (i, x) in v.iter().enumerate() { ... }
for x in &mut v { *x += 1; }                // 得到指向元素的 &mut（GC 支持内部指针）
if let Some(x) = opt { ... } else { ... }
while let Some(x) = stack.pop() { ... }
let Some(x) = opt else { return; };         // let-else

// 闭包（按引用捕获，被捕获的变量会逃逸到堆上，同 Go；没有 move 关键字）
let add = |a: i32, b: i32| -> i32 { a + b };
let inc = |x| x + 1;

// unsafe
let b = unsafe { *raw_ptr };
```

- **循环变量每次迭代都是新的一份**（同 Go 1.22 之后）：闭包按引用捕获，`for i in 0..n { thread::spawn(|| work(i)); }` 里每个闭包拿到的是各自那一轮的 `i`
- `?` 用于 `Result<T, E>` 和 `Option<T>`，错误值通过 `From` trait 自动转换（同 Rust）
- 运算符重载通过 `std::ops` 里的 trait：`Add Sub Mul Div Rem Neg Not BitAnd BitOr BitXor Shl Shr` 和对应的 `*Assign`、`Index IndexMut`；比较用 `std::cmp` 的 `PartialEq PartialOrd`
- `==` 比较的是值（通过 `PartialEq`）；比较是不是同一个对象用 `std::ptr::eq(a, b)`
- **`Deref` / `DerefMut`**（同 Rust）：
  - 方法调用时，沿 `Deref` 链自动解引用，所以 `String` 可以直接调用 `str` 的方法，`Vec<T>` 可以直接调用切片的方法
  - 解引用强制转换：期望 `&str` 的地方可以传 `&String`，期望 `&[T]` 的地方可以传 `&Vec<T>`
- 没有 `++` / `--`，没有三元运算符

### 1.8 语句与绑定

```rust
let x = 5;                  // 不可变绑定
let mut y: i64 = 0;         // 可变绑定
let (a, b) = pair;          // 解构
let x = x * 2;              // 允许遮蔽（shadowing）
defer file.close();         // 函数返回时执行（按 LIFO 顺序；panic 时也会执行）
defer { log("exit"); }
```

- 语句以 `;` 结束；`if`、`match`、`loop`、`for`、`while` 和块作为语句时可以不写分号
- `defer` 绑定在**函数**上（同 Go），不是绑定在块上
- `defer expr.method(args)` 的接收者和参数**在执行 `defer` 语句时就求值**，函数返回时才调用（同 Go）；`defer { .. }` 块里的代码则全部推迟到返回时才执行

### 1.9 模式匹配

```rust
match v {
    0 => ...,                               // 字面量
    1 | 2 | 3 => ...,                       // 多选
    4..=9 => ...,                           // 区间
    n @ 10..=99 => ...,                     // 绑定 + 子模式
    n if n < 0 => ...,                      // 守卫
    _ => ...,                               // 通配
}
match p {
    Point { x: 0.0, y } => ...,             // 结构体模式，字段简写
    Point { x, .. } => ...,                 // 忽略其余字段
}
match e {
    Shape::Circle { r } => ...,
    Shape::Rect(w, h) => ...,
    Shape::Empty => ...,
}
match t { (0, _) => ..., (x, y) => ... }
match s { [] => ..., [first, .., last] => ..., [x] => ... }   // 切片模式
match opt_ref { Some(&Point { x, .. }) => ..., None => ... }
match name { "admin" => ..., _ => ... }     // 字符串字面量模式
```

- `match` 必须穷尽，编译器做穷尽性检查和不可达分支检查
- 模式可以用在 `let`、函数参数、`if let`、`while let`、`let-else`、`for` 里
- 没有 `matches!`，判断"是否匹配"用 `if let` 或 `match`

### 1.10 crate、模块与导入（同 Rust）

```
myapp/
  navi.toml              # [package] name = "myapp"；[dependencies] ...
  src/
    main.navi            # 二进制 crate 的根（库 crate 是 src/lib.navi）
    parser.navi          # mod parser;
    parser/
      lexer.navi         # 在 parser.navi 里写 mod lexer;
      ast.navi
```

```rust
mod parser;                                 // 声明子模块
mod tests { use super::*; #[test] fn t() { ... } }   // 内联模块

use std::fmt::{self, Display};
use crate::parser::{Parser, Token as Tok};
use super::util::*;                         // glob 导入
pub use crate::parser::Ast;                 // 重新导出
```

- **crate 是编译单位**，也是孤儿规则和 `pub(crate)` 的边界；crate 之间不能循环依赖，crate 内的模块可以互相引用
- 文件查找规则同 Rust：`mod foo;` 加载 `foo.navi` 或 `foo/mod.navi`
- 路径以 `crate::`、`self::`、`super::`、外部 crate 名或 `std::` 开头
- **条件编译**：`#[cfg(target_os = "windows")]` 可以加在条目和 `mod` 声明上，`#![cfg(...)]` 写在文件开头作用于整个模块。可用的条件有 `target_os`、`target_arch`、`test`、`debug_assertions`、`feature = "..."`
- **为了编译速度**（crate 粒度比 Go 的包粗，所以 crate 内部也要并行）：
  - crate 内所有文件并行解析
  - 先收集所有条目的签名，再并行检查各个函数体
  - 后端按函数并行
  - M9 加上 crate 内以函数为粒度的增量缓存

### 1.11 并发

API 照着 Rust std，底层是绿色线程（G-M-P 调度）：

```rust
use std::thread;
use std::sync::mpmc::{channel, sync_channel};

let h = thread::spawn(|| heavy(42));        // 启动绿色线程，h: JoinHandle<i32>
let r = h.join().unwrap();                  // 等它结束并拿到返回值

let (tx, rx) = sync_channel::<i32>(16);     // 有界 channel；sync_channel(0) 是无缓冲的
let (tx2, rx2) = channel::<String>();       // 无界 channel
thread::spawn(|| { tx.send(1).unwrap(); tx.close(); });   // 闭包直接捕获，不需要 move
let v: Result<i32, RecvError> = rx.recv();  // 阻塞；channel 关闭且取空后返回 Err
for v in rx.iter() { ... }                  // 一直接收到 channel 关闭为止

select {
    v = rx.recv()  => println(`got ${v:?}`),   // v: Result<T, RecvError>
    tx2.send(s)    => {},
    default        => {},                   // 可选；有 default 就不阻塞
}
```

- **为什么用 `mpmc` 而不是 Rust 常用的 `mpsc`**：Rust 的 `Receiver` 只能有一个，靠的是它不能被拷贝；navi 的值都能拷贝，所以 Receiver 天然可以被多个线程共享，`mpmc`（Rust std 里也有这个模块）的语义正好对得上
- **显式 `close()`**：Rust 靠"所有 `Sender` 都被 drop"来关闭 channel，navi 没有 Drop，所以加一个 `Sender::close()`。这是相对 Rust std 的少数新增 API 之一
- `Sender` / `Receiver` / `JoinHandle` 都是引用语义
- `select` 是 navi 新增的关键字（Rust std 没有对应的东西）
- 没有 `Send` / `Sync`：不做 auto trait，数据竞争由用户负责（同 Go）
- **线程里的 panic 会结束整个进程**（同 Go），这和 Rust 不同：Rust 里只有那个线程结束，`join()` 返回 `Err`。navi 的 `join()` 保留 Rust 的签名 `-> thread::Result<T>`，等以后有了 `panic::catch_unwind` 才会真的返回 `Err`。这一条记进 `spec/std-diff.md`
- `Mutex`、`RwLock`、`Condvar`、原子操作和定时器这一版不做
- 不提供 `async` / `await`

### 1.12 错误处理与 panic

- 可恢复的错误用 `Result<T, E>` 和 `?`；`Option<T>` 也可以用 `?`
- ``panic(`bad value ${x}`)``：类型是 `!`，展开当前线程的栈并依次执行 `defer`；没有被捕获就打印栈回溯并结束进程
- 捕获 panic 用 Rust 的 `std::panic::catch_unwind`（相当于 Go 的 recover），这一版不做
- 数组越界、除以零、debug 构建下的整数溢出、`unwrap()` 失败，都会 panic

### 1.13 模板字符串（字符串插值，JS 写法）

```rust
let name = "navi";
let s: String = `hello ${name}!`;           // 插值调用 Display
let t = `sum = ${a + b}, first = ${v[0]}`;  // ${} 里可以是任意表达式
let d = `${point:?}`;                       // : 后面是格式说明：? 调用 Debug
let p = `${point:#?}`;                      // 多行美化的 Debug
let f = `${pi:.3} ${n:>8} ${n:08} ${n:x} ${n:#b}`;   // 精度、对齐、补零、进制（规则同 Rust）
let m = `第一行
第二行 ${x}`;                               // 可以跨行，换行原样保留
let e = `价格 \${price}，反引号 \``;        // 转义
```

- 只有反引号字符串会插值，`"..."` 里的 `${` 就是普通字符
- **格式说明的分隔**：`${...}` 里最外层的单个 `:` 之后是格式说明。`::` 路径不受影响；如果表达式本身在最外层含有单个 `:`（比如带类型标注的闭包），就给表达式加括号
- 编译器在编译期把模板展开成对 `fmt::Formatter` 的直接调用，运行时不再解析格式
- 暂不支持 JS 的标签模板（`` tag`...` ``）

**模板字符串的类型由上下文决定**（和整数字面量的类型由上下文决定是同一个思路）：
- 期望类型是 `fmt::Arguments`、`impl Display`，或者带 `Display` 约束的类型参数时，模板字符串是 `fmt::Arguments`：一个**不分配内存的惰性格式化对象**。`${}` 里的表达式在创建时就求值，但要等写入时才格式化
- 其他情况（包括没有期望类型）下是 `String`
- 所以 ``println(`x=${x}`)`` 和 ``f.write_fmt(`...`)`` 都直接写到输出，不会先构造中间的 `String`；而 ``let s = `...`;`` 得到的是 `String`

**每种格式说明对应一个 trait**，给自己的类型实现对应的 trait 就能用在插值里（同 Rust）：

| 写法 | trait | 内置 derive |
|---|---|---|
| `${x}` | `fmt::Display` | 无（必须手写） |
| `${x:?}` / `${x:#?}` | `fmt::Debug` | `#[derive(Debug)]` |
| `${x:x}` / `${x:X}` | `fmt::LowerHex` / `fmt::UpperHex` | 无 |
| `${x:o}` / `${x:b}` | `fmt::Octal` / `fmt::Binary` | 无 |
| `${x:e}` | `fmt::LowerExp` | 无 |

- 格式说明只有上表这一组固定写法，编译期就检查；类型没实现对应的 trait 时，报错指向 `${}` 的位置
- 实现了 `Display` 的类型自动获得 `to_string()`（blanket impl）

**为自己的类型实现插值**：
```rust
use std::fmt::{self, Display, Debug, Formatter};

struct Point { x: f64, y: f64 }

impl Display for Point {
    fn fmt(&self, f: &mut Formatter) -> fmt::Result {
        f.write_fmt(`(${self.x}, ${self.y})`)   // 模板在这里是 fmt::Arguments，不分配
    }
}

// Debug 一般用 derive；要手写时用 Formatter 的辅助方法，它们会自动处理 {:#?} 的多行缩进
impl Debug for Point {
    fn fmt(&self, f: &mut Formatter) -> fmt::Result {
        f.debug_struct("Point").field("x", &self.x).field("y", &self.y).finish()
    }
}

// 支持宽度、对齐等格式说明
struct Name(String);
impl Display for Name {
    fn fmt(&self, f: &mut Formatter) -> fmt::Result {
        f.pad(&self.0)                     // 按 ${n:>10}、${n:*^10} 的要求补齐
    }
}

let p = Point { x: 1.0, y: 2.0 };
println(`p = ${p}, debug = ${p:?}`);       // p = (1, 2), debug = Point { x: 1.0, y: 2.0 }
let s: String = p.to_string();
```

`Formatter` 的 API（名字和签名同 Rust）：
- 写入：`write_fmt(Arguments)`、`write_str(&str)`；`fmt::Write` trait 另有 `write_char(char)`
- 读格式说明：`width()`、`precision()`、`align()`、`fill()`、`alternate()`（`#`）、`sign_plus()`
- 套用格式说明：`pad(&str)`、`pad_integral(..)`
- Debug 辅助：`debug_struct`、`debug_tuple`、`debug_list`、`debug_set`、`debug_map`
- `fmt::Result` 就是 `Result<(), fmt::Error>`，可以用 `?`

### 1.14 标准库：向 Rust std 看齐，并结合 GC 调整

没有单独的 `core`，只有一个 `std` crate。runtime 也在 std 里面，是私有模块 `std::rt`（见第 4 节），所以不存在 std 和 runtime 互相依赖的问题。

**设计规则**（这一版和以后补全标准库时都按这个来）：

1. **默认照搬 Rust std**：模块路径、类型名、trait 名、方法名、参数顺序和返回类型都和 Rust 一样，比如 `Option::ok_or`、`Vec::extend_from_slice`、`str::split_whitespace`、`HashMap::entry`。Rust 文档里查得到的东西，在 navi 里写法相同
2. **签名里的生命周期全部去掉**：`fn iter(&self) -> Iter<'_, T>` 变成 `fn iter(&self) -> Iter<T>`
3. **闭包参数**：`F: FnMut(T) -> U` 变成 `fn(T) -> U`
4. **只因为没有 GC 才存在的东西不提供**：
   - 所有权和借用相关：`Box`、`Rc`、`Arc`、`Weak`（Rust 版）、`Cell`、`RefCell`、`Cow`、`Pin`、`ManuallyDrop`、`mem::drop`
   - auto trait：`Copy`、`Send`、`Sync`
5. **依赖 Drop 的 API 改成显式写法**：
   - 需要释放的资源（文件、socket 等）提供 `close()`，配合 `defer` 使用
   - 锁不返回 guard，改成 `lock()` / `unlock()`，另外提供 `with_lock(|v| ..)`
   - channel 加 `close()`
6. **消费 `self` 的方法保留名字**（如 `into_iter`、`into_bytes`），因为没有 move，语义变成"拷贝或共享"；和 `Box` 有关的方法（如 `into_boxed_slice`）不提供
7. **宏改成函数**：
   - `println!` 系列 → `std::io::{print, println, eprint, eprintln}`
   - `panic!` → `std::panic::panic`
   - `assert!` 系列 → `std::panic::{assert, assert_eq, assert_ne}`
   - 以上都在 prelude 里；`format!` 就是模板字符串本身
8. **GC 语言特有的功能**放在新模块 `std::gc`，比如 `gc::collect()`、弱引用 `gc::Weak<T>`、finalizer。这一版不做
9. **每一处和 Rust 不同的地方**，都在 `spec/std-diff.md` 里列出来

**这一版只实现语言和测试离不开的子集**，用的都是 Rust 的路径：

| 模块 | 内容 | 依赖它的语言特性 |
|---|---|---|
| `std::option` / `std::result` | `Option<T>`、`Result<T, E>` 及常用方法 | `?`、`let-else` |
| `std::str` / `std::string` | `str` 的基本方法、`String` | 字符串字面量、模板字符串 |
| `std::vec` / `std::slice` | `Vec<T>`、切片方法 | `for`、切片、`Deref` |
| `std::fmt` | 格式化 trait、`Formatter`、`Arguments`、`fmt::Write` | 模板字符串、`#[derive(Debug)]` |
| `std::iter` | `Iterator`、`IntoIterator`、`Peekable` 和常用适配器 | `for` 循环 |
| `std::ops` / `std::cmp` | 运算符 trait、`Deref` / `DerefMut`、`Range*`、比较 trait | 运算符、区间、解引用 |
| `std::clone` / `std::default` / `std::hash` / `std::convert` | `Clone`、`Default`、`Hash` / `Hasher`、`From` / `Into` / `TryFrom` / `AsRef` | `derive`、`?` 的错误转换 |
| `std::mem` / `std::ptr` | `size_of`、`swap`、`replace`、`take`；`ptr::eq` 和原始指针方法 | unsafe |
| `std::thread` | `spawn`、`JoinHandle` | 并发 |
| `std::sync::mpmc` | `channel`、`sync_channel`、`Sender`、`Receiver` | `select` |
| `std::io`（仅打印） | `print`、`println`、`eprint`、`eprintln` | 测试输出 |
| `std::panic` | `panic`、`assert`、`assert_eq`、`assert_ne`、`Location` | 断言 |
| `std::intrinsics` | 编译器内建函数（见 1.16） | runtime |
| `std::prelude` | 同 Rust 的 prelude，加上上面的打印和断言函数 | 自动导入 |

- 这一版不做：`std::collections`（`HashMap` 等）、`fs`、`io` 的读写、`env`、`process`、`path`、`net`、`time`、`sync` 里除 mpmc 以外的部分。这些到自举之前单独做计划
- ``println(`x = ${x}`)``：参数类型是 `impl Display`，所以 `println("hi")` 也可以
- `panic`、`assert` 系列带 `#[track_caller]` 属性，编译器会自动传入调用者的文件和行号
- runtime 的汇编写在单独的 `*_arm64.s` / `*_amd64.s` 文件里，由 navi 内置的汇编器处理；navi 代码里用 `#[asm] fn name(..);`（没有函数体）声明（同 Go）

### 1.15 属性

| 属性 | 作用 |
|---|---|
| `#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Default)]` | 编译器内置实现（属性，不是宏） |
| `#[test]`、`#[bench]` | 只有 `navi test` / `navi bench` 才会编译 |
| `#[cfg(...)]`、`#![cfg(...)]` | 条件编译 |
| `#[inline]`、`#[inline(never)]`、`#[cold]` | 内联提示 |
| `#[track_caller]` | 隐式传入调用位置 |
| `#[repr(C)]`、`#[repr(u8)]` | 内存布局 |
| `#[link(name = "...")]`、`#[export_name = "..."]`、`#[asm]` | FFI 和 runtime 用 |
| `#[deprecated]`、`#[allow(...)]`、`#[must_use]` | 诊断 |

### 1.16 unsafe 与 FFI

- `unsafe fn`、`unsafe { }`、`unsafe impl`
- 原始指针 `*const T` / `*mut T`：解引用、指针运算（`p.add(n)`）、和整数互相转换都要在 `unsafe` 里
- `extern "C"` 的函数按各平台的 C ABI 调用；传给 C 的参数只能是基本类型、原始指针和 `#[repr(C)]` 结构体
- `extern "C" fn` 函数指针类型（见 1.3）用于 C 回调；navi 的 `fn(A) -> B` 带着闭包环境，不能直接传给 C
- runtime 专用的编译器内建函数放在 `std::intrinsics`（如 `type_desc::<T>()`、`gc_write_barrier`），只有 std 内部（包括 `std::rt`）能用

### 1.17 和 Rust 的差异汇总

| 删掉 | 新增 | 改变 |
|---|---|---|
| 生命周期、借用检查、move 语义 | `defer` | `&T` / `&mut T` 变成 GC 指针，可以随意保存 |
| `Copy`、`Drop`、`Send`、`Sync`、`Box`、`Rc`、`Arc`、`Cell`、`RefCell`、`Cow`、`Pin` | `select`；channel 的 `close()` | 集合类型是引用语义 |
| `async` / `await` | 模板字符串 `` `${x}` `` | 闭包统一为 `fn(A) -> B` 类型，没有 `Fn*` trait |
| 所有宏（`macro_rules!`、过程宏、`println!` 等） | `static mut` 不需要 `unsafe` | 打印、断言、panic 都是普通函数 |
| GAT、HRTB、特化、`union` | `#[asm]` 加 `.s` 文件 | `thread::spawn` 启动的是绿色线程 |
| | `std::gc`（以后） | 锁、文件等资源要显式释放 |

### 1.18 完整示例

```rust
// src/main.navi
mod lexer;

use std::thread;
use std::sync::mpmc::sync_channel;
use crate::lexer::{lex, Token};

fn main() {
    let src = "let x = 42 + y1";
    let (tx, rx) = sync_channel::<Vec<Token>>(1);
    thread::spawn(|| { tx.send(lex(src)).unwrap(); });

    let tokens = rx.recv().unwrap();
    let mut idents = Vec::new();
    for t in tokens.iter() {
        if let Token::Ident(name) = t {
            idents.push(name.clone());
        }
    }
    println(`${tokens.len()} tokens, identifiers: ${idents:?}`);
}
```

```rust
// src/lexer.navi
#[derive(Debug, Clone)]
pub enum Token { Ident(String), Num(i64), Sym(char) }

pub fn lex(src: &str) -> Vec<Token> {
    let mut out = Vec::new();
    let mut chars = src.chars().peekable();
    while let Some(&c) = chars.peek() {
        match c {
            ' ' | '\n' => { chars.next(); }
            '0'..='9' => {
                let mut n = 0i64;
                while let Some(&d @ '0'..='9') = chars.peek() {
                    n = n * 10 + (d as i64 - '0' as i64);
                    chars.next();
                }
                out.push(Token::Num(n));
            }
            c if c.is_alphabetic() => {
                let mut s = String::new();
                while let Some(&a) = chars.peek() {
                    if !a.is_alphanumeric() { break; }
                    s.push(a);
                    chars.next();
                }
                out.push(Token::Ident(s));
            }
            _ => { out.push(Token::Sym(c)); chars.next(); }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn lex_num() { assert_eq(lex("42").len(), 1); }
}
```

---

## 2. 泛型的实现（GC shape 共享 + 字典，同 Go）

- **shape** 大致等于底层类型，所有 GC 指针类型归为同一个 shape：基本类型和值类型实际上是按类型单独生成代码的，只有指针类型共用一份
- 每个 shape 实例多一个隐藏参数，就是字典指针。每种**具体**的实例化对应一份编译期生成的只读字典，里面有：
  - 类型参数和派生类型的类型描述符
  - trait 方法表
  - 子字典
- 和 Go 不同：navi 需要支持带自己类型参数的方法（如 `Iterator::map<B>`），所以字典按"方法级"生成
- **导出数据里放类型检查过的泛型函数体**（同 Go），因为下游可能用新的值类型 shape 来实例化；下游直接从这些函数体生成 MIR，不重新做类型检查
- 实例按 (函数, shape 元组) 去重；不同 crate 各自生成的同一个实例，在链接时合并（DUPOK / COMDAT）

---

## 3. 编译器架构（stage0，Go 实现）

```
source ─▶ lexer ─▶ parser ─▶ AST           （crate 内各文件并行）
       ─▶ resolve（mod 树、use、名称解析）
       ─▶ 收集签名 ─▶ sema（类型推断与检查、trait 解析、泛型定义处检查、
                           穷尽性检查、模板字符串展开、derive）  （按函数并行）
       ─▶ MIR（与架构无关的 SSA IR；泛型按 GC shape 生成实例 + 字典）
       ─▶ const 求值（MIR 解释器）
       ─▶ 少量优化（常量折叠、DCE、内联小函数、逃逸分析）
       ─▶ lower（按 arch：amd64 / arm64）
       ─▶ 线性扫描寄存器分配 + stack map 生成   （按函数并行）
       ─▶ 指令编码 ─▶ 目标文件（Mach-O / ELF / COFF）
       ─▶ 链接（初期：本机用系统链接器，交叉编译用 lld；后期：自研链接器）
```

**链接器**：
- 本机：macOS 用 `ld`，Linux 用 `cc` / `ld`，Windows 用 `link.exe`
- 交叉编译：系统链接器不能跨格式（mac 的 `ld` 链接不了 ELF / PE），所以统一用 LLVM 的 `lld`（`ld.lld` / `lld-link` / `ld64.lld`），作为一个可选的外部依赖
- 交叉编译到 macOS 还需要 SDK 里的 `.tbd` 存根文件，到 Windows 需要 `kernel32.lib` 等导入库。M9 的自研链接器直接生成导入表，就不再需要这些文件

- 类型推断只在函数体内部做（双向推断，同 Rust）；函数签名必须写全类型，这样各个函数可以并行检查

**为编译速度做的设计**（贯穿全程）：
- 手写 lexer 和递归下降 parser；字符串 intern；AST 和 IR 用 arena 加索引，不用指针图
- **两级并行**：
  - crate 依赖图里互不依赖的 crate 并行编译
  - crate 内部：文件解析、函数体检查、后端都按文件或函数并行
- **导出数据（export data）**：每个 crate 编译完写出 `.navih`，内容包括类型、签名、trait impl 表、类型检查后的泛型函数体；下游 crate 只读它，不重新解析源码
- **增量编译缓存**：
  - crate 级：以（源码 hash + 依赖的导出数据 hash + 编译选项）为 key，保存 `.o` 和 `.navih`
  - M9 再加 crate 内以函数为粒度的缓存：签名没变的函数，只重新编译函数体变了的那些
- 默认优化等级很低（与 Go 相当），不引入 LLVM 那样的重型管线
- 没有宏展开这一步，parser 一遍就能得到完整的 AST

**目录结构（stage0）**：
```
navi2/
  go.mod                      module navi
  cmd/navi/                   CLI：build / run / check / test / bench
  internal/syntax/            token, lexer（含模板字符串）, ast, parser
  internal/types/             mod 树解析, resolve, sema, trait 解析, 穷尽性检查, derive, 模板展开
  internal/mir/               SSA IR, builder, passes, 逃逸分析, 泛型 shape 实例化
  internal/mir/interp/        MIR 解释器（const 求值 + 测试用的 --interp）
  internal/backend/arm64/     lowering, regalloc, 编码, .s 汇编器
  internal/backend/amd64/
  internal/obj/macho|elf|coff 目标文件写出
  internal/link/              调用系统链接器（后期替换为自研）
  internal/abi/               内部 ABI + C ABI（SysV, Win64, AAPCS64, Apple arm64）
  lib/std/                    std crate（这一版只做 1.14 列出的子集）
  lib/std/src/rt/             runtime，std 的私有模块 std::rt（navi + .s，见第 4 节）
  tests/                      golden 测试（*.navi + 期望输出）
  bench/                      编译速度基准
  spec/                       语言规范（第 1 节展开）
```

---

## 4. 运行时（用 navi 本身写，参照 Go runtime）

runtime 是 std 的私有模块 `std::rt`，一开始就用 navi（`unsafe` + `std::intrinsics` + `#[asm]` 加 `.s` 文件）写，**不依赖 C 编译器**，这样自举之后也不需要 C。因为和 std 在同一个 crate 里，runtime 可以直接用 `Option`、`Result` 等类型；但 runtime 自己的代码不能分配 GC 对象，这条由编译器对 `std::rt` 做检查（类似 Go 的 `//go:nosplit`、`//go:nowritebarrier` 约束）。

- **系统接口层**：
  - linux 直接 syscall
  - macOS 走 libSystem（syscall 号不稳定）
  - windows 走 kernel32/ntdll
  - 三者从 M3 起就分开抽象
- **调用 C 函数前切换到系统栈**：绿色线程的栈很小，而且会被复制，C 代码不知道这些。所以调用 libSystem / kernel32 和用户的 `extern "C"` 函数时，先切换到 M 的系统栈（g0）再调用（同 Go 的 `asmcgocall`）；C 回调 navi 时反过来切换。这件事要和 M3 的 ABI 一起设计
- **内存分配器**：size-class + 每个 P 一份缓存（tcmalloc / Go mallocgc 的思路）
- **GC**：精确、不移动对象的 mark-sweep，**支持内部指针**（用于 `&mut v[i]`、字段引用、切片）
  - 原始指针（`*const T` / `*mut T`）字段也当作可能指向堆的指针来扫描：std 内部要靠它实现通过 `&self` 修改的共享结构（channel 等，相当于 Rust 的 `UnsafeCell`）
  - v1：stop-the-world 精确标记（类型指针位图 + 编译器生成的 stack map）
  - v2：并发三色标记 + 写屏障（编译器在指针写入处插入屏障）
- **调度器**：G-M-P 模型
  - 栈可以增长：函数序言做栈检查，不够时复制整个栈。所以需要精确的 stack map，并依赖逃逸分析保证没有堆指针指向栈
  - 抢占先在函数序言处协作式实现，以后再加基于信号的异步抢占
- **defer / panic 展开**：每个函数维护自己的 defer 链；panic 时沿栈展开并执行 defer，打印栈回溯
- **channel / select / timer / netpoller**：netpoller 推到自举之后
- 调试符号（DWARF / PDB）推到自举之后

---

## 5. 里程碑

| # | 内容 | 验收 |
|---|---|---|
| **M0** | 把第 1 节写成 `spec/`；初始化仓库（git、go.mod、CI 骨架） | spec 覆盖第 1 节全部语法；CI 能跑 `go test ./...` |
| **M1** | 前端：lexer（含模板字符串）、parser、mod 树和 resolve、sema（类型推断、trait 解析、`Deref` 方法解析、泛型定义处检查、穷尽性检查、模板展开、derive）；`navi check`。**同时用 navi 写出 1.14 里 std 子集的类型和签名**，函数体可以先写成 `todo()` | `tests/syntax` 覆盖 1.1–1.16 的每一种语法；1.18 的示例能通过检查；违反约束的泛型代码在定义处报错 |
| **M2** | MIR + 泛型 shape 实例化 + 字典 + 简单优化；MIR 解释器（`const` 求值，以及 `navi run --interp`）。**补全 std 子集的函数体**；解释器用宿主（Go）实现内存分配、打印、`thread::spawn`（用 goroutine）和 channel，作为 `std::rt` 的替身 | golden 测试在解释器上全过，包括线程和 `select` 的用例（之后作为后端的正确性基准）；`Vec<&A>` 和 `Vec<&B>` 共用同一份实例 |
| **M3** | **arm64 + Mach-O** 后端、`.s` 汇编器、内部 ABI 和 C ABI（含切换到系统栈调用 C），最小 `std::rt`（bump 分配、无 GC、单线程、defer），系统 `ld` 链接 | `hello world`、`fib`、结构体/枚举/match/闭包/defer/模板字符串的用例生成原生二进制并得到正确输出 |
| **M4** | 分配器 + STW 精确 GC + stack map + panic 展开 | 大量分配的压力测试内存稳定；GC 正确性测试（对象图校验） |
| **M5** | **amd64** 后端 + ELF + COFF；覆盖 linux-x64/arm64、macos-x64、windows-x64 | 5 个目标在 CI 矩阵（GitHub Actions：ubuntu x64 / arm64、macOS arm64、macOS Intel、windows x64）上 golden 测试全过；装了 lld 的情况下能从 mac 交叉编译到其余 4 个目标 |
| **M6** | G-M-P 调度器、可增长栈、`std::thread::spawn`、`std::sync::mpmc`、`select`；并发 GC + 写屏障 | 并发测试（百万个绿色线程、生产者-消费者）；压力测试稳定 |
| **M7** | **补全标准库（这一版暂缓）**：到这里再单独做计划，按 1.14 的规则向 Rust std 看齐。自举至少要用到 `fs`、`env::args`、`process`、`collections::HashMap`、`io::BufReader` / `BufWriter` | 另做计划时再定 |
| **M8** | **自举**：用 navi 重写编译器（按 stage0 的模块结构逐个移植） | stage0 编译出 stage1 → stage1 编译出 stage2 → stage2 编译出 stage3，**stage2 与 stage3 二进制逐字节相同** |
| **M9** | 性能：自研链接器（Mach-O 需要 ad-hoc 代码签名）、函数粒度的增量缓存、并行调优；补上自举后的特性（const 泛型、`panic::catch_unwind`、i128、DWARF） | 见第 6 节性能基准 |

说明：
- M3 选 darwin-arm64，是因为宿主机就是这个平台，迭代最快
- 这一版计划实际执行到 M6 为止：5 个平台、GC、并发，以及 std 的最小子集都做完。M8 自举依赖 M7 的标准库，要等标准库单独做完计划再继续
- 从 M8 开始，新功能只加到 navi 版本里，stage0 冻结，只用于引导
- 自举前的语言特性要克制：只做写编译器用得到的，其余推到自举之后

---

## 6. 编译性能目标与度量

- `bench/gen.go` 生成合成项目（10k / 100k / 500k 行；分别有"少数大 crate"和"多个小 crate"两种结构），同时生成**等价的 Go 代码**，对比 `go build` 的耗时
- 指标：冷编译耗时、无改动时的重编译耗时、改一个函数体后的重编译耗时、峰值内存
- 目标：M5 时冷编译耗时不超过 `go build` 的 1.5 倍，M9 时与 `go build` 持平
- 从 M1 起每个里程碑都跑基准并记录，退化超过 10% 就当作 bug 处理
- 内置 `navi build --timings`，输出各阶段耗时

---

## 7. 验证方式

- **golden 测试**：`tests/**/*.navi`，文件头写 `// expect: ...` 或配套 `.out` 文件；同一份用例分别在 MIR 解释器、各个后端、各个平台上运行，结果必须一致
- **语法覆盖**：第 1 节的每个代码块都转成测试用例，保证 spec 和实现一致
- **错误用例**：`// error: line:col message`，校验诊断信息
- **单元测试**：Go 侧各包用 `go test`；navi 侧用 `navi test`
- **CI 矩阵**：每次提交在 5 个目标平台上运行；交叉编译出的产物在对应平台的 runner 上执行
  - macOS Intel 的 runner：原来的 `macos-13` 已经退役，到 M5 时用 GitHub 当时还提供的 Intel 标签；如果已经没有 Intel runner 了，就退而用 arm64 mac 上的 Rosetta 2 运行 x64 产物
- **自举校验**：M8 之后，每次提交都比对 stage2 和 stage3
- **GC / 调度器**：用带 `NAVI_GCDEBUG` 的校验模式运行（每次 GC 后检查堆的一致性），再加压力测试

---

## 8. 主要风险

- **字典调用的运行时开销**：指针类型的泛型代码调用 trait 方法要多一次间接调用（Go 泛型也有这个问题，迭代器链之类的代码会受影响）
  - 对策：调用处类型已知、被调函数又很小时，内联并去掉字典
  - 如果实测确实成为瓶颈，再考虑对热点做单态化
- **Rust 式类型系统比 Go 的重**：双向类型推断、trait 求解、`Deref` 自动解引用、穷尽性检查，都比 Go 的类型检查慢。"和 Go 一样快"的目标有一部分风险在前端
  - 对策：函数签名必须写全类型，推断只在函数体内；trait 求解结果按 (trait, 类型) 缓存；impl 不允许重叠，求解时不回溯
  - 从 M1 起就单独测前端耗时，和 `go vet` / `go build` 的类型检查阶段对比
- **`&T` 是浅只读**：从 `&T` 里拷贝出的集合句柄仍然可写，熟悉 Rust 的用户可能以为 `&Vec` 是完全只读的。spec 里要专门讲，lint 可以对"从 `&T` 拷出集合再修改"给出警告
- **闭包统一为 `fn` 类型**：迭代器链里的每个闭包都是间接调用，和上一条叠加以后影响更大，需要内联来缓解
- **crate 粒度的重编译**：改大 crate 里的一个函数，整个 crate 都要重新编译。M9 前靠 crate 内并行顶住，M9 加函数粒度的增量缓存
- **对齐 Rust std 和 GC 实际情况之间的取舍**：每偏离 Rust 一处，迁移代码和查文档都会多一处坑。所以偏离只限于 1.14 列出的几类原因，并且全部记录在 `spec/std-diff.md` 里
- **没有 move 和 Drop**：习惯 Rust 的用户可能以为赋值是 move、离开作用域会自动释放资源，需要在 spec 和诊断里讲清楚（例如提示用 `defer`）
- **没有宏**：序列化之类只能靠内置 derive 或外部代码生成工具。以后需要的话，可以在自举之后加只读的编译期反射
- **模板字符串里 `:` 的歧义**：最外层单个 `:` 之后是格式说明，表达式本身含单个 `:` 时要加括号，诊断要给出明确提示
- **精确 GC + 可增长栈 + 逃逸分析 + 内部指针**四者耦合：从 M3 开始就要生成 stack map，否则 M4 / M6 要返工
- **macOS arm64 代码签名**：用系统 ld 时会自动处理；自研链接器必须自己生成 ad-hoc 签名
- **Windows**：COFF、Win64 ABI、SEH unwind 信息和栈探测（`__chkstk`）都比其他平台麻烦，M5 要预留足够时间
- 自举前的语言特性容易膨胀，需要严格控制范围
