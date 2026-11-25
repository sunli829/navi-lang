# Navi 语言规范（草案）

> 状态：M0 草案。本文件是 M1 的 parser 和 sema 的依据；修改语法时先改这里。
> 和 Rust std 的差异单独记录在 [std-diff.md](std-diff.md)。

总原则：**写法尽量和 Rust 一致**，Rust 程序员不用查文档就能读懂；**但要符合 GC 语言的实际情况**，Rust 里只因为没有 GC 才存在的东西不照搬。和 Rust 不同的地方有四类：
- 删掉为所有权和借用服务的部分
- 加入 GC 和并发需要的部分
- 用模板字符串和普通函数代替宏
- 为了编译速度做的简化

## 1.1 词法

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

## 1.2 基本类型

| 类型 | 说明 |
|---|---|
| `i8 i16 i32 i64 isize i128` / `u8 u16 u32 u64 usize u128` | 整数；它们的字面量最大到 `u64::MAX`，更大的值用运算得到（如 `u128::MAX`、`1 << 100`）；对齐是 8 字节 |
| `f32 f64` | 浮点 |
| `bool` | `true` / `false` |
| `char` | 4 字节的 Unicode 标量值 |
| `()` | 单元类型 |
| `!` | never 类型（`panic(..)`、`loop {}`、`return` 的类型） |

- 无后缀的整数字面量默认推断为 `i32`，浮点默认为 `f64`，与 Rust 相同
- **整数溢出**：一律检查，溢出就 panic（同 Swift；Rust 只在 debug 构建检查）；要回绕时显式用 `wrapping_*`，另外有 `checked_*`、`saturating_*`、`overflowing_*` 方法
- **只有一种构建模式**（同 Go）：没有 debug / release 之分，也没有 `cfg(debug_assertions)`，`debug_assert` 和 `assert` 一样一直生效；可执行文件总是带调试信息（DWARF），`navi build -s` 去掉它（同 Go 的 `-ldflags=-s`）
- **类型转换**只能用 `as`，没有隐式的数值转换

## 1.3 复合类型与引用（和 Rust 差别最大的部分）

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
  - 普通字符串字面量的类型是 `&str`；模板字符串的类型总是 `fmt::Arguments`，要 `String` 时显式写 `.to_string()`（见 1.13）
- **集合**：`Vec<T>`、`String`（以及以后的 `HashMap`、`HashSet`、`VecDeque`、`BTreeMap`）内部是指向堆对象的引用，所以**拷贝以后共享同一个容器**（类似 Go 的 map、Java 的 ArrayList），要独立副本时用 `.clone()`。这样避免了 Go 切片那种"共享底层数组、长度却各自独立"的陷阱
- **引用上的 `.clone()` 克隆的是被引用的值**（同 Rust）：`x: &T` 或 `x: &mut T` 时 `x.clone()` 调用 `T::clone`，得到一个 `T`；`T` 没有实现 `Clone` 时是编译错误（Rust 里 `&mut T` 不能克隆，`&T` 会得到引用的拷贝并给出警告，navi 两种都报错）。要复制引用本身，直接赋值。引用本身仍然实现 `Clone`，用于泛型约束和 `#[derive(Clone)]` 里的引用字段，拷贝的是引用
- 不支持 `union`；FFI 用 `#[repr(C)] struct`

## 1.4 条目（item）

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
extern "C" {                                // 不写 #[link]：系统的 C 库
    fn write(fd: i32, buf: *const u8, n: usize) -> isize;
}
#[link(name = "z")]                         // 别的动态库（见 1.16）
extern "C" {
    fn crc32(crc: u64, buf: *const u8, len: u32) -> u64;
}
#[export_name = "navi_cb"]
extern "C" fn callback(x: i32) -> i32 { x }
```

- **可见性**：同 Rust，`pub`、`pub(crate)`、`pub(super)`；不写就是对当前模块及其子模块可见
- `const fn`：只允许纯计算，不能分配 GC 对象、不能做 IO；求值有步数上限

## 1.5 impl 与 trait

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
    fn describe(&self) -> String { `area ${self.area()}`.to_string() }   // 默认方法
}

impl Figure for Circle {
    type Unit = Meters;
    const SIDES: u32 = 0;
    fn area(&self) -> f64 { 3.14159 * self.r * self.r }
}

impl<T: Display> ToString for T {           // blanket impl
    fn to_string(&self) -> String { `${self}`.to_string() }
}
```

- 方法调用会自动取引用和自动解引用（`p.len()` 对 `Point`、`&Point`、`&mut Point` 都能用）；调用 `&mut self` 方法时，接收者必须是 `let mut` 绑定或者 `&mut` 引用
- 固有 impl（`impl Point`）只能写在定义这个类型的 crate 里
- trait impl 遵守孤儿规则：trait 或类型至少有一个是本 crate 定义的（同 Rust）
- **impl 不允许重叠**；不支持特化、负 impl、auto trait
- **trait 对象**：`&dyn Trait`。navi 没有 `Sized`，所以规则和 Rust 稍有不同：任何 trait 都能做成 `dyn`，但只有"可经 dyn 调用"的方法进入 vtable——接收者是 `&self` / `&mut self`、没有自己的类型参数、参数和返回值里不出现 `Self`。其余方法（如 `Iterator::map`）不能通过 `dyn` 调用，效果等同 Rust 给它们加了 `where Self: Sized`。`dyn` 类型必须写出 trait 的全部关联类型（`dyn Iterator<Item = u8>`）
- **没有 `Drop`**，资源释放用 `defer`（见 1.8）

## 1.6 泛型

```rust
fn largest<T: PartialOrd>(xs: &[T]) -> &T { ... }
fn print_all<I>(it: I) where I: Iterator, I::Item: Display { ... }
struct Stack<T> { items: Vec<T> }
impl<T: Clone> Stack<T> { fn peek(&self) -> Option<T> { ... } }
enum Tree<K: Ord, V> { Leaf, Node(&Tree<K, V>, K, V, &Tree<K, V>) }

fn evens(v: &[i32]) -> impl Iterator<Item = i32> { ... }   // 返回位置的 impl Trait
fn sum(it: impl Iterator<Item = i32>) -> i32 { ... }       // 参数位置的 impl Trait
trait Shape { fn parts(&self) -> impl Iterator<Item = i32>; }  // trait 方法返回 impl Trait

let v = parse::<i64>("42");                 // turbofish
let n = <T as Figure>::SIDES;               // 完全限定路径
```

- 泛型函数体**在定义处只检查一次**，只能使用约束里 trait 提供的方法和运算符；报错位置就在写错的地方
- 自举前支持：trait 约束、`where`、`+` 组合约束、默认方法、关联类型和关联常量、blanket impl、`impl Trait`、带自己类型参数的方法（如 `Iterator::map<B>`）、默认类型参数（`trait PartialEq<Rhs = Self>`，有默认值的参数必须排在最后，同 Rust）
- const 泛型（M9 起，只有 navi）：`struct Matrix<const R: usize, const C: usize>`、`fn sum<const N: usize>(xs: [i32; N])`、`impl<T, const N: usize> From<[T; N]> for Vec<T>`。参数的类型是整数、`bool` 或 `char`；实参是字面量、`{ 常量表达式 }` 或别的 const 参数，也可以从数组类型推断；函数体里 `N` 是值，`[x; N]` 可用。关联常量也可以做数组长度：`[u8; T::N]`、`[u8; Self::LEN]`（`T` 的约束里声明了 `N` 的 trait；`T` 确定后取 impl 里的值）。还不支持含参数的常量表达式（如 `[T; N + 1]`）
- trait 方法可以返回 `impl Trait`（M9 起，只有 navi；同 Rust 1.75）：每个 impl 的方法决定实际类型（可以写自己的 `impl Trait`，也可以写具体类型），经泛型调用时按 Self 找到那个 impl。`impl Trait` 必须是整个返回类型，trait 里的这种方法不能有默认方法体
- 不做：GAT、高阶 trait bound（`for<'a>`）、特化
- **闭包没有专门的 `Fn` / `FnMut` / `FnOnce` trait**，统一用函数类型 `fn(A) -> B`。例如 `fn map<B>(self, f: fn(Self::Item) -> B) -> Map<Self, B>`。这样泛型和 shape 共享都简单得多

## 1.7 表达式

所有块都是表达式，最后一个不带分号的表达式就是块的值。

```rust
// 字面量与构造
Point { x: 1.0, y }                         // 字段简写
Point { x: 0.0, ..origin }                  // 结构体更新语法
Shape::Rect(1.0, 2.0)
[1, 2, 3]   [0u8; 256]   (a, b)   ()
`(${p.x}, ${p.y})`                          // 模板字符串，类型是 fmt::Arguments（见 1.13）

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
if let Some(x) = a && let Ok(y) = f(x) && y > 0 { ... }   // let 链（同 Rust 2024），while 里也可以用

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

## 1.8 语句与绑定

```rust
let x = 5;                  // 不可变绑定
let mut y: i64 = 0;         // 可变绑定
let (a, b) = pair;          // 解构
let x = x * 2;              // 允许遮蔽（shadowing）
(a, b) = (b, a);            // 解构赋值（同 Rust）：元组、数组/切片、结构体、元组结构体，可以嵌套，可以用 _ 和 ..
P { x, y: v[i], .. } = p;   // 目标可以是变量、字段、下标、*p；先求右边，再从左到右赋值
defer file.close();         // 函数返回时执行（按 LIFO 顺序；panic 时也会执行）
defer { log("exit"); }
```

- 语句以 `;` 结束；`if`、`match`、`loop`、`for`、`while` 和块作为语句时可以不写分号
- `defer` 绑定在**函数**上（同 Go），不是绑定在块上
- `defer expr.method(args)` 的接收者和参数**在执行 `defer` 语句时就求值**，函数返回时才调用（同 Go）；`defer { .. }` 块里的代码则全部推迟到返回时才执行

## 1.9 模式匹配

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

- `match` 必须穷尽，编译器做穷尽性检查；前面的分支已经覆盖的分支给出警告 `unreachable pattern`（不影响编译，同 Rust）。有守卫的分支不算覆盖；或模式只要有一个分支可达就不警告。std 里的警告不显示；命中构建缓存时照样再打印一遍
- 模式可以用在 `let`、函数参数、`if let`、`while let`、`let-else`、`for` 里
- 没有 `matches!`，判断"是否匹配"用 `if let` 或 `match`

## 1.10 crate、模块与导入（同 Rust）

```
myapp/
  navi.toml              # [package] name = "myapp"；[dependencies] ...（必须有）
  navi.lock              # git 依赖的 commit（navi 写）
  target/                # 构建输出（navi build 写到 target/myapp）
  src/
    main.navi            # 二进制 crate 的根（库 crate 是 src/lib.navi）
    parser.navi          # mod parser;
    parser/
      lexer.navi         # 在 parser.navi 里写 mod lexer;
      ast.navi
  tests/
    cli.navi             # 集成测试：每个文件单独编译，可以 use 本 crate（库）
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
- **条件编译**：`#[cfg(target_os = "windows")]` 可以加在条目和 `mod` 声明上，`#![cfg(...)]` 写在文件开头作用于整个模块。可用的条件有 `target_os`、`target_arch`、`test`（`feature = "..."` 目前恒为假；只有一种构建模式，所以没有 `debug_assertions`，见 1.2）
- **依赖**写在 `navi.toml` 的 `[dependencies]` 里（写法同 Cargo），可以是本地路径，也可以是 git 仓库的某个 tag（或 `rev = "完整 commit"`，都不写就是默认分支）：

  ```toml
  [dependencies]
  foo = { path = "../foo" }
  bar = { git = "https://github.com/x/bar", tag = "v1.2.0" }
  ```

  - 键名就是代码里用的名字（`use bar::...`），可以和依赖自己的包名不同；依赖必须是库 crate（`src/lib.navi`），依赖的依赖按它自己的 `navi.toml` 递归加载，路径相对于写它的 crate
  - 依赖以源码形式和程序一起编译（同 Go），没有二进制库
  - 同一个仓库的不同版本可以同时存在（同 Cargo）：它们是不同的 crate，类型互不相同；报错时第二个版本显示为 `bar@v1.3.0`
  - git 依赖解析到的 commit 记在项目根目录（workspace 的根目录）的 `navi.lock` 里，应提交进仓库：tag 被挪动也不改变构建；`navi update` 重新解析所有 tag
  - 也可以写 `{ workspace = true }`，从 workspace 的 `[workspace.dependencies]` 继承（见下）；不支持 Cargo 那样从 registry 取版本（`foo = "1.0"`）
  - `navi build` 和 `navi test` 会自动拉取缺少的依赖（需要装有 git），检出放在缓存目录的 `pkg/` 下（或 `NAVI_PKG`），所有项目共用；语言服务器只用已经拉取的
- **项目**（同 Cargo）：
  - 一个 crate 就是一个带 `navi.toml` 的目录，`[package]` 的 `name` 必填（字母、数字、`_`、`-`，不以数字开头；代码里 `-` 写成 `_`）。navi 从当前目录（或命令给的目录）往上找最近的 `navi.toml`
  - `[package]` 还可以写 `version`、`description`、`authors`（字符串数组）、`license`、`repository`、`homepage`（同 Cargo），代码里用 `std::build::package_version()`、`package_description()`、`package_authors()`（以 `, ` 连接）、`package_license()`、`package_repository()`、`package_homepage()`（和 `package_name()`）取到，是调用所在 crate 的包的（同 Cargo 的 `CARGO_PKG_*`）；构建时从外面传值用 `navi build --var 名字=值`（`run`、`test` 也可以），代码里 `std::build::var("名字")` 得到 `Option<&str>`（名字要写字面量；同 Go 的 `-ldflags -X`）。这些在编译时就是常量，改了版本或 `--var` 的值会重新编译用到它们的 crate
  - 单个 `.navi` 文件不需要 `navi.toml`，可以直接 `navi build x.navi` / `navi run x.navi` / `navi test x.navi`（同 `go run x.go`），但不能有依赖
  - `navi new 名字` / `navi init`（`--lib` 建库，`--vcs none` 不建 git 仓库）创建 crate：`navi.toml`、`src/main.navi`（或 `src/lib.navi`）、`.gitignore`，不在 git 仓库里时 `git init`
  - `navi build` 把程序写到项目根目录的 `target/名字`（`-o` 另指），库只做检查；`navi run [-- 参数]` 构建并运行；`navi test` 运行单元测试（`#[test]`）和 `tests/*.navi` 集成测试：集成测试每个文件是一个单独的 crate，能用本 crate 的依赖，本 crate 是库时还能 `use` 它，是程序时环境变量 `NAVI_BIN_EXE_名字` 是它的路径（同 Cargo 的 `CARGO_BIN_EXE_`）。`navi test a.navi b.navi dir ...` 可以给多个路径（最后一个参数不是文件或目录时是过滤串），照 `go test` 每个路径一个进程并行（`-j N`，默认 CPU 数），按路径顺序打印结果；`-timeout 时长`（默认 10m，`0` 不限）超时的测试程序被杀掉；测试程序和 `navi build` 一样走构建缓存。一个测试程序里的 `#[test]` 照 Rust 并行运行（`navi test -- --test-threads=N`，默认 CPU 数），每个测试的输出和 panic 消息被捕获，只在失败时打印（`-- --nocapture` 照常输出）
- **workspace**（同 Cargo）：根目录的 `navi.toml` 写 `[workspace]`：

  ```toml
  [workspace]
  members = ["app", "crates/*"]      # * 匹配一段路径里的任意字符
  exclude = ["crates/old"]

  [workspace.dependencies]
  regex2 = { git = "https://github.com/x/regex2", tag = "v1.2.0" }
  util = { path = "crates/util" }    # 路径相对于 workspace 根目录
  ```

  - 成员的 `navi.toml` 写 `regex2 = { workspace = true }`，版本只在根目录写一次
  - 整个 workspace 共用根目录的一个 `navi.lock` 和 `target/`
  - 在根目录运行 `navi build` / `navi test` 处理全部成员，`-p 名字` 选一个；在成员目录（或它下面）运行只处理这个成员
  - 根目录可以只有 `[workspace]`，也可以同时有 `[package]`，自己也是一个成员
  - 在 workspace 下面、但既不在 `members` 也不在 `exclude` 里的 crate 是错误；`navi new` 在 workspace 里创建 crate 时把它加进 `members`
  - git 依赖的仓库本身是 workspace 时，用其中和键名同名的成员（同 Cargo）
- **为了编译速度**（crate 粒度比 Go 的包粗，所以 crate 内部也要并行）：
  - crate 内所有文件并行解析
  - 先收集所有条目的签名，再并行检查各个函数体
  - 后端按函数并行
  - M9 加上 crate 内以函数为粒度的增量缓存

## 1.11 并发

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
- **线程里的 panic 只结束这个线程**（同 Rust，M9 起）：`join()` 返回 `Err(PanicPayload)`。早先的版本里它会结束整个进程（同 Go）
- `Mutex`、`RwLock`、`Condvar`、原子操作和定时器这一版不做
- 不提供 `async` / `await`

## 1.12 错误处理与 panic

- 可恢复的错误用 `Result<T, E>` 和 `?`；`Option<T>` 也可以用 `?`
- ``panic(`bad value ${x}`)``：类型是 `!`，展开当前线程的栈并依次执行 `defer`；没有被捕获就打印栈回溯并结束进程
- 捕获 panic 用 Rust 的 `std::panic::catch_unwind(f)`（相当于 Go 的 recover）：返回 `Result<R, PanicPayload>`，`f` 里的 `defer` 在返回 `Err` 之前都已执行；`resume_unwind(payload)` 重新抛出
- 数组越界、除以零、debug 构建下的整数溢出、`unwrap()` 失败，都会 panic

## 1.13 模板字符串（字符串插值，JS 写法）

```rust
let name = "navi";
let s: String = `hello ${name}!`.to_string();   // 插值调用 Display；String 要显式转
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
- 标签模板（JS 的 `` tag`...` ``）只有 `` quote`...` ``（`std::derive::quote`，写派生宏时生成代码用，见 docs/derive.md）：模板的文字是代码，`${x}` 插入 `x` 的代码（`std::derive::ToCode`），不能带格式说明；编译器检查模板的括号是否配对

**模板字符串的类型总是 `fmt::Arguments`**，不随上下文变化（相当于 Rust 的 `format_args!`）：
- `fmt::Arguments` 实现了 `Display`，是一个**惰性格式化对象**：`${}` 里的表达式在创建时就求值，值按 navi 的规则拷贝进去，但要等写入时才格式化
- 打印、断言、panic 这类函数的参数是 `impl Display`，所以 ``println(`x=${x}`)`` 和 ``f.write_fmt(`...`)`` 都直接写到输出，不会先构造中间的 `String`
- **要 `String` 时显式转换**：``let s = `...`.to_string();``（相当于 Rust 的 `format!`）。没有隐式转换，期望 `String` 的地方写模板字符串是类型错误。和 Rust 一样，除了解引用（`Deref` 强制转换）以外的操作都是显式的
- 因为是惰性的，集合这类引用语义的值在创建之后、写出之前的修改会体现在结果里：
  ```rust
  let mut v = Vec::from([1]);
  let a = `v = ${v:?}`;      // 拷贝的是 v 这个句柄，不是内容
  v.push(2);
  println(a);                // v = [1, 2]
  let s = a.to_string();     // 在这一刻格式化，之后 v 再变 s 也不变
  ```
  需要固定下当时的内容就立即 `.to_string()`

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

## 1.14 标准库：向 Rust std 看齐，并结合 GC 调整

没有单独的 `core`，只有一个 `std` crate。runtime 也在 std 里面，是私有模块 `std::rt`（见 [docs/plan.md](../docs/plan.md) 第 4 节），所以不存在 std 和 runtime 互相依赖的问题。

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
   - 元编程用派生（`#[derive(...)]`，见 docs/derive.md），没有运行时反射；序列化照搬 serde（`std::serde`），`std::json`、`std::csv` 建在它上面
9. **每一处和 Rust 不同的地方**，都在 `spec/std-diff.md` 里列出来
10. **约束的写法同 Rust std 的习惯**：一个类型参数只有一个约束时写在参数上（`fn max<T: Ord>`、`I: IntoIterator<Item = T>`）；有参数带多个约束（`T: Hash + Eq`），或约束里用到同一条目的另一个类型参数（`A: PartialEq<B>`）时，这个函数或 impl 的约束全部写到 `where` 里，和签名写在同一行：`pub fn assert_eq<A, B>(left: A, right: B) where A: PartialEq<B> + Debug, B: Debug {`

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

## 1.15 属性

| 属性 | 作用 |
|---|---|
| `#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Default)]` | 编译器内置的派生（同 Rust）；`Serialize`、`Deserialize`（`std::serde`）、`FromAttrs`（`std::derive`）和用户的派生宏要先 `use` |
| `#[derive_macro(Name, attrs = [ns, ...])]` | 把库 crate 里的一个 `pub fn(&std::derive::TypeInfo) -> Result<Code, Error>` 定义成派生宏 `Name`，`attrs` 是它接受的字段和类型属性（docs/derive.md）；结构体、枚举、变体、字段上编译器不认识的属性必须有某个派生接受，否则报错 |
| `#[test]`、`#[bench]` | 只有 `navi test` / `navi bench` 才会编译；`navi test` 运行测试时设置环境变量 `NAVI` 为 navi 自身的路径（同 Cargo 的 `CARGO`），测试可以用它编译程序 |
| `#[should_panic]`、`#[should_panic(expected = "...")]` | 测试必须 panic（同 Rust），给了 `expected` 时 panic 信息必须包含它 |
| `#[cfg(...)]`、`#![cfg(...)]` | 条件编译 |
| `#[inline]`、`#[inline(never)]`、`#[cold]` | 内联提示 |
| `#[track_caller]` | 隐式传入调用位置 |
| `#[repr(C)]`、`#[repr(u8)]` | 内存布局 |
| `#[link(name = "...", kind = "...")]` | `extern` 块的函数来自哪个动态库（见 1.16） |
| `#[export_name = "..."]`、`#[asm]` | FFI 和 runtime 用 |
| `#[intrinsic]` | 声明由编译器实现的函数（没有函数体），只有 std 能用 |
| `#[link_name = "..."]` | `extern "C"` 函数在链接时使用的符号名（同 Rust） |
| `#[nonblocking]` | 标在 `extern "C"` 块或其中的函数上：调用时不按"阻塞"处理（见 1.16） |
| `#[deprecated]`、`#[allow(...)]`、`#[must_use]` | 诊断 |

## 1.16 unsafe 与 FFI

- `unsafe fn`、`unsafe { }`、`unsafe impl`
- 原始指针 `*const T` / `*mut T`：解引用、指针运算（`p.add(n)`）、和整数互相转换都要在 `unsafe` 里
- `extern "C"` 的函数按各平台的 C ABI 调用；传给 C 的参数只能是基本类型、原始指针和 `#[repr(C)]` 结构体
- **只链接动态库，不链接外来的目标代码**（静态库、C 编译器产出的目标文件都不支持，`kind = "static"` 报错）。所以 navi 不需要 C 编译器和系统链接器，自研链接器从任何平台生成任何平台的可执行文件（同 Go 不用 cgo 时）：
  - `extern` 块不写 `#[link]` 时，函数来自系统的 C 库：macOS 的 libSystem，Linux 的 libc、libm、libpthread，Windows 首字母大写的来自 kernel32.dll、其余来自 ucrtbase.dll
  - `#[link(name = "z")]`（`kind` 默认 `"dylib"`）：链接器只记下"这个块的函数来自这个库"，生成导入，不需要库文件本身（同 Go 的 `cgo_import_dynamic`、Rust 的 `kind = "raw-dylib"`）。名字到文件：macOS 是 `/usr/lib/libz.dylib`，Linux 是 `libz.so`，Windows 是 `z.dll`；`name` 里写了路径、`.so` 或 `.dll` 时原样使用（Linux 上没装开发包时要写带版本的 `"libz.so.1"`，macOS 上可以写 `"@rpath/libfoo.dylib"`）
  - `#[link(name = "CoreFoundation", kind = "framework")]`：macOS 的 framework
  - `name = "c"` 就是系统的 C 库
  - `--linker cc` 改用系统链接器时，这些库作为 `-l`、`-framework` 传给它
- `extern "C" fn` 函数指针类型（见 1.3）用于 C 回调；navi 的 `fn(A) -> B` 带着闭包环境，不能直接传给 C
- **C 函数可能阻塞或运行很久**（读写、等待、长时间计算），所以对 `extern "C"` 块里声明的函数，每次直接调用期间，当前绿色线程都算作"阻塞"（同 Go 的 cgo 调用进入系统调用状态）：
  - GC 不等它返回；其他线程照常分配和收集
  - 代价是每次调用多几纳秒
  - 确定很快返回的函数（如 `labs`、`memcpy`）可以给函数或整个 `extern` 块标 `#[nonblocking]`，省掉这几纳秒；标错（实际会阻塞）的后果是阻塞期间 GC 等它
  - 经 `extern "C" fn` 指针的调用也按阻塞处理
- 按阻塞处理的 C 调用在所在系统线程的调度栈上运行（同 Go 的 cgo 调用切到 g0 栈），栈空间是系统线程的；标了 `#[nonblocking]` 的直接在绿色线程的栈上运行，可用的栈空间至少 32KB
- 某个系统线程上的 C 调用阻塞超过约 20 微秒、又有别的绿色线程等着运行时，runtime 的监控线程会另起一个系统线程接手（同 Go 的 sysmon），所以 `NAVI_MAXPROCS=1` 时阻塞的 C 调用也不会卡住其他线程
- **C 回调 navi**：navi 写的 `extern "C" fn`（有函数体）可以传给 C 当回调（它的值是一个按 C ABI 调用的入口）：
  - 参数和结果只能是数字、`bool`、`char` 和指针，参数要全部能放进寄存器（arm64 整数和浮点各 8 个；x86-64 System V 整数 6 个、浮点 8 个；Windows 共 4 个）
  - 回调在该系统线程专用的回调绿色线程上运行，有自己可增长的栈；发起 C 调用的绿色线程在回调期间保持阻塞，它的栈不动
  - 回调里可以分配、触发 GC、再调用 C（C 还可以再回调），也可以等待锁和 channel（等待时这个系统线程睡眠，同 Go 回调期间锁定线程）
  - 只有按阻塞处理的 C 调用里才能回调：`#[nonblocking]` 的 C 函数回调 navi 时进程会 abort（同 Go 的 `#cgo nocallback`）；不是 navi 启动的系统线程也不能回调
  - 回调里的 panic 不能穿过 C 的栈帧
- runtime 专用的编译器内建函数放在 `std::intrinsics`（如 `type_desc::<T>()`、`gc_write_barrier`），只有 std 内部（包括 `std::rt`）能用

## 1.17 和 Rust 的差异汇总

| 删掉 | 新增 | 改变 |
|---|---|---|
| 生命周期、借用检查、move 语义 | `defer` | `&T` / `&mut T` 变成 GC 指针，可以随意保存 |
| `Copy`、`Drop`、`Send`、`Sync`、`Box`、`Rc`、`Arc`、`Cell`、`RefCell`、`Cow`、`Pin` | `select`；channel 的 `close()` | 集合类型是引用语义 |
| `async` / `await` | 模板字符串 `` `${x}` `` | 闭包统一为 `fn(A) -> B` 类型，没有 `Fn*` trait |
| 宏（`macro_rules!`、函数式和属性过程宏、`println!` 等；只有派生宏） | `static mut` 不需要 `unsafe` | 打印、断言、panic 都是普通函数 |
| GAT、HRTB、特化、`union` | `#[asm]` 加 `.s` 文件 | `thread::spawn` 启动的是绿色线程 |
| | `std::gc`（以后） | 锁、文件等资源要显式释放 |

## 1.18 完整示例

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
