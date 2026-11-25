# 派生宏（设计）

## 目标

- 用户可以定义自己的 `#[derive(X)]`，用 navi 写，例如 JSON 序列化、builder、ORM 映射。
- 生成的是普通的 navi 代码，和手写的 impl 一样检查、内联、优化；不靠运行期反射，所以没有逐字段查类型描述的开销。
- 内置的 `Debug`、`Clone`、`PartialEq`、`Eq`、`PartialOrd`、`Ord`、`Hash`、`Default` 也改成派生宏，和用户的走同一套机制。现在 `mir/shims.navi` 里按具体类型现场生成方法体的那部分可以去掉。
- 不做 Rust 那样的函数式宏和任意改写代码的属性宏：只做派生，以及派生宏声明的字段属性。
- 去掉运行期反射（`std::reflect`）：`std::json`、`std::csv` 改成照搬 serde 的 `Serialize`/`Deserialize`，用户类型写 `#[derive(Serialize, Deserialize)]`；编译器生成的类型描述只留 GC 需要的部分。

和 Rust 过程宏的区别：

- 宏拿到的不是 token 流，而是类型检查器收集好的结构：字段、字段类型、泛型、属性，还能查询某个类型是否实现了某个 trait。
- 宏生成 impl 块，以及它自己要用的辅助类型和函数（如 serde 的 visitor；名字自己起，别和用户的冲突），不能生成 use、mod、extern，不能改写原来的代码。展开一轮就结束，不需要反复迭代。
- 展开结果按输入缓存，并存进导出数据（`.navih`，见 docs/crates.md），下游和重新编译都不用再展开。

## 定义和使用

派生宏是库 crate 里一个带 `#[derive_macro]` 的函数，返回生成的代码（完整的例子见 `tests/derive/macros`）：

```navi
use std::derive::{TypeInfo, Code, Error, FromAttrs, quote};

// 字段属性 #[json(rename = "...", skip)] 的形状：解析和报错由 FromAttrs 生成。
#[derive(FromAttrs)]
struct JsonField { rename: Option<String>, skip: bool }

#[derive_macro(Serialize, attrs = [json])]
pub fn serialize(t: &TypeInfo) -> Result<Code, Error> {
    // 结构体、元组结构体、枚举一样写：each_variant 生成 match 和每个字段的绑定。
    let body = t.each_variant(|v| {
        let mut fields: Vec<Code> = Vec::new();
        for b in v.bindings().iter() {
            let a: JsonField = b.field().parse_attrs("json")?;
            if a.skip { continue; }
            let key = a.rename.unwrap_or(b.field().ident().to_string());
            fields.push(quote`w.key(${key}); ${b}.serialize(w);`.at(b));
        }
        Ok(quote`w.begin_object(); ${fields} w.end_object();`)
    })?;
    let method = quote`fn serialize(&self, w: &mut ::myjson::Writer) { ${body} }`;
    // impl 头（泛型参数、给类型参数加的约束）由 impl_for 生成。
    Ok(t.impl_for("::myjson::Serialize", method))
}
```

- `#[derive_macro(Name, attrs = [ns, ...])]`：`Name` 是 `#[derive(...)]` 里写的名字；`attrs` 是这个宏认领的字段和类型属性的命名空间（`#[json(...)]`）。没有被任何派生认领的属性报错，拼错的属性不会被悄悄忽略。
- 使用：派生名和普通条目一样经 `use` 引入（`use myjson::Serialize;`），然后 `#[derive(Serialize)]`，也可以写路径 `#[derive(myjson::Serialize)]`。没有全局注册表；同名的 trait 和派生宏可以从同一个路径引入（类型命名空间里的 trait，派生命名空间里的宏）。
- 宏函数和到它的各级模块都要是 `pub`；宏只能在别的 crate 里用（宏所在的 crate 要先编成程序）。
- 内置派生（Rust 的那八个：`Clone`、`Debug`、`PartialEq`、`Eq`、`PartialOrd`、`Ord`、`Hash`、`Default`）不用引入，写法不变。`Serialize`、`Deserialize`（`std::serde`）和 `FromAttrs`（`std::derive`）是 std 里的派生宏，和 Rust 一样要引入（`use std::serde::{Serialize, Deserialize};` 同时引入 trait 和宏）。模块里显式引入的同名派生宏（`use myjson::Serialize;`）优先；glob 引入不会遮住内置派生。

## 生成代码：`quote`

`` quote`...` `` 是带标签的模板（JS 的 tagged template）：普通函数 `std::derive::quote` 的调用写法，不是宏（标签只能是 `quote`）。模板可以跨行。编译器把它改写成 `quote(&[各段文字], &[std::derive::ToCode::to_code(&(插值)), ...])`：文字原样是代码（其中的转义也原样留给生成的代码，只有 `` \` `` 和 `\${` 变成 `` ` `` 和 `${`），插值不能带格式说明。`Code` 是文本加上各段文字来自用户代码的位置；拼接时只在需要的地方加空格（两个词或两个运算符之间，以及一般的分隔处；`f(x)`、`a.b`、`Vec<T>`、`x: T`、`a::b`、`&x` 不加），所以插值是一个个记号，不要写在生成代码的字符串字面量里面。

插值按值的类型变成代码（`ToCode` trait），不靠字符串拼接：

| 插入的值 | 生成的代码 |
|---|---|
| `String`、`&str` | 字符串字面量，自动转义（`${key}` 生成 `"id"`） |
| `Ident` | 标识符（`self.${f.ident()}`） |
| `TypeRef`、`Binding` | 对应的类型；绑定的变量（位置在字段上） |
| 整数、`bool`、`char` | 字面量 |
| `Code` | 原样嵌入 |
| `Vec<T: ToCode>`、切片 | 依次展开（相当于 Rust `quote!` 的 `#(...)*`） |
| `Option<T: ToCode>` | `None` 是空代码 |

要生成的代码里本身有模板字符串时，用 spec 已有的转义：`` \` `` 和 `\${`。

**片段先存进变量**（同 Rust：`quote!` 里不套 `quote!`，先生成 `TokenStream` 再用 `#body` 插入）：`${}` 里只写普通表达式，不写模板。每个片段是一个 `Code` 值，存进变量后在外层插入：

```navi
let fields = t.each_construct(|b| quote`${b}.clone()`);
let body = quote`fn clone(&self) -> Self { ${fields} }`;
t.impl_for("::std::clone::Clone", body)
```

闭包里的 `quote` 不在任何 `${}` 里；每一步一个变量，一行只写一个 `quote`。词法分析器不需要支持模板里套模板。

**位置映射到用户代码**（同 Rust `quote!` 保留 span）：

- `Ident`、`TypeRef`、`Field`、`Generics` 等来自用户代码的值，插进去后生成的 token 保留它们在用户源文件里的位置。比如字段类型没有实现 `Serialize`，`self.${f.ident()}.serialize(w)` 的错误指向用户结构体里的那个字段。
- 模板自己的 token 默认位置是用户写的 `#[derive(X)]`，报错时附上出错的那行生成代码（`note: in the code derive X for T generated: ...`）。
- `` quote`...`.at(x) `` 把整段模板的默认位置改成 `x`（字段、变体、类型）的位置，相当于 Rust 的 `quote_spanned!`：针对整条语句的错误也能落在对应字段上。
- LSP：生成代码里插入的标识符可以跳回用户代码。

**语法提前检查**：编译宏所在的 crate 时，编译器检查每个 `quote` 模板本身能否分成记号、括号是否配对，错误在写宏时就报，不用等展开。

**名字解析**：生成的代码放在类型所在的模块里解析。宏里引用外部 crate 的条目**必须**从 crate 的根写起：`::myjson::Serialize`、`::std::fmt::Debug`（`::名字` 只认外部 crate 和本 crate 自己，不认任何局部名字），这样类型所在的模块里不管有没有叫 `std`、`myjson` 的东西，生成的代码都指向同一个条目。Rust 里这只是写过程宏的惯例，navi 里是规则：编译器检查宏自己写的代码（用户写在属性里、宏原样插入的部分不算，它们本来就按用户的模块解析），路径以某个 crate 的名字（`std` 或依赖）开头而前面没有 `::` 就报错（`derive X generated a path starting with the crate std: ...`）。引用本 crate 用 `crate::`。（std 的 `FromAttrs` 暂时例外：自举用的发布版还不认 `::std`，等 tools/bootstrap-version 换成认它的版本再改。）内置派生同样写 `::std::...`。局部变量名的卫生（生成代码里的 `__d`、`m` 不和用户的名字冲突）还没有做，宏里用 `__` 开头的名字。

## 写宏的辅助

写派生宏的时间大多不花在拼代码上，而在下面几件事上（Rust 里分别靠 synstructure、darling 和 cargo expand），这里都内置：

- **按绑定遍历**：结构体、元组结构体、枚举不用分别处理，也不用手写解构模式。
  - `t.each(|b| ...)`：生成 `match *self { 模式 => { 每个绑定的代码 } ... }`，`b` 插入时是那个字段的绑定（`__binding_0` 等）。
  - `t.each_variant(|v| ...)`：按变体生成，闭包返回 `Result<Code, Error>`（可以用 `?`）；`v.bindings()` 是它的字段绑定，`v.name()` 是变体名，`v.pattern()`、`v.construct(值)` 是它的模式和构造。结构体当作一个变体。
  - `t.each_construct(|b| ...)`：生成构造同一变体的表达式（`Clone` 这类）。
  - 两个值一起比较时（`PartialEq`、`Ord`），`t.each_pair(other, |a, b| ..., differ)` 同时解构两边，变体不同时是 `differ`。
- **impl 头**：`t.impl_for("path::Trait", items)` 生成 `impl<...> path::Trait for Type<...> where ...`，类型参数自动加上这个 trait 的约束（同 Rust 的 derive）；`impl_for_with(trait, bound, items)` 给类型参数加的是 `bound`（空代码就不加）。
- **属性解析**：`#[derive(FromAttrs)]` 描述一个属性的形状（具名字段的结构体，每个字段按它的类型的 `FromAttrValue` 取同名的键，同 darling 的 `FromMeta`：`from_value` 是 `key = value`，`from_word` 是只写了键（`bool` 是 true），`from_missing` 是没写（`Option` 是 None，`bool` 是 false，其余报缺少）；派生不看类型的名字），`f.parse_attrs::<T>("json")`（字段、变体、类型都有）把所有 `#[json(...)]` 的参数解析成它；未知的键、重复的键、缺少的键、值的类型不对都自动报错，位置在属性上。值可以是 `String`、`bool`、`i64`、`i32`、`usize`，或 `std::derive::Raw`（路径等，照写的文本）。`FromAttrs` 本身是 std 里的派生宏（`std::derive::from_attrs_derive`）。
- **查看展开结果**：`navi expand [-p 名字] [目录或文件] [类型]` 打印 crate（或其中某个类型）的派生展开后的代码（格式化后），相当于 `cargo expand`；内置派生的也打印。
- **测试**：宏可以在普通的 `#[test]` 里调用：`TypeInfo::of::<T>()` 由编译器内建函数（`type_info`）在编译期构造，`code.text()` 是生成的文本，用来比较（`tests/run/derive_api.navi`）。

## 宏能看到什么：`std::derive`

- `TypeInfo`：类型名、所在模块路径、可见性、类型上的属性（不含 `derive`；`///` 注释作为 `#[doc(text = "...")]` 传来，`doc()` 取出，`TypeInfo`、`Variant`、`Field` 都有；`TypeInfo::of` 拿不到源码，没有）；泛型参数和它们的约束、where 子句（`generics()`，`self_type()` 用在 impl 头上）；`is_enum()`、`variants()`（结构体是一个变体）、`fields()`（结构体的）。
- `Variant`：名字、种类（具名、元组、单元）、字段、属性、写出来的判别值、在声明里的序号。
- `Field`：名字和 `ident()`（元组字段为 `0`、`1`……）、序号、可见性、类型、属性（`has_attr`、`parse_attrs`）、位置。
- `Attr`：名字和参数（`key`、`key = 值`，值是字符串、整数、布尔或照写的文本），宏也可以不用 `FromAttrs` 自己看。
- `TypeRef`：字段类型，按写出来的语法：可以拆开（路径和它的泛型参数、引用、数组、元组），插进 `quote` 就是这个类型。
- `Error`：`Error::at(x, msg)` 在 `x`（字段、类型、属性）的位置报错，例如某个字段的类型不支持。

`TypeInfo` 是纯数据，可以序列化：用户的宏在单独的进程里运行（见下），编译器把它发过去，`Code` 连同各段的位置发回来。编码很简单（字符串是 `长度:字节`，整数是 `i数;`，列表是 `[...]`），两边各写一份（`compiler/src/derive/info.navi`、`std::derive` 里的 `Dec`）。

## 什么时候展开

和 Rust 一样，按语法展开，在解析函数体里的名字之前（`resolve/expand.navi`）。

1. 一个 crate 的条目收集完时，内置派生马上展开，除非模块显式引入了同名的东西（可能是派生宏）。
2. 剩下的名字等所有 crate 的 `use` 解析完，在派生命名空间（`MACRO_NS`）里找：找到派生宏就按宏 crate 分组；找不到但是内置的名字，就按内置展开；都不是就报错。
3. 每个宏 crate 编成一个程序，一次收到它所有的请求，返回生成的代码或错误。
4. 生成的代码作为一个文件解析成条目（不能有 use、mod、extern）。文件名是原文件名加上 `[derive X for T@位置]`，所以生成条目的稳定符号名（symbols::def_id）不会和别的类型的重复，crate 换了位置也不变；文件的位置映射回用户的文件（`source::Origin`）。
5. 条目收集进类型所在的模块，之后的名字解析、收集签名、检查函数体、MIR、代码生成对它们一视同仁。
6. 结构体、枚举、变体、字段上编译器不认识的属性，要有某个派生接受（宏的 `attrs`，如 std 的 `Serialize`、`Deserialize` 接受 `serde`），否则报错。

宏看到的是语法：字段的类型是写出来的样子（结构化的，可以拆开路径、泛型参数、引用、数组、元组），不是类型检查后的类型。所以 `implements` 这类需要类型信息的查询暂时没有；以后需要时，作为类型收集之后的第二阶段再加。

生成代码里的错误照常报，位置映射回用户代码，附上那行生成的代码。

## 怎么执行

- **内置派生**写在编译器里（`compiler/src/derive/`），在编译器进程内直接调用。这样 std 自己的类型可以用它们，不需要先编出 std 再运行宏（自举不成环）。它们和派生宏的写法一样（`TypeInfo` 进，`quote` 写的 `Code` 出，用 `std::derive` 的 `each_variant`、`each_pair`、`impl_for` 等），只是在编译器里直接调用；生成代码的位置照样映射回用户代码。v0.0.4 发布之后才这样写（编译器的源码要能被上一个发布版编译），之前是拼字符串。生成比原来拼字符串慢（std 的 343 个派生 15.7ms，原来约 3ms），但读进来的 crate 不再展开（见“缓存”）。
- **用户的派生宏**：编译器为宿主平台把宏所在的 crate 和一个生成的 main（调用 `std::derive::serve`，按名字分派到各个宏函数）编成一个小程序（交叉编译时也是宿主平台）。编译由一个子进程 `navi derive-build` 做（编译器的目标平台等是全局状态），用构建缓存：main.navi 放在缓存目录下按内容命名的地方，路径每次相同。一个 crate 的所有展开请求写进一个文件发给它（序列化的 `TypeInfo` 列表），从标准输出收回 `Code`。每个宏 crate 每次编译最多启动一次。
- 宏必须是确定的：相同的 `TypeInfo` 得到相同的输出，不读写文件、不看时间和环境。文档里写明，不强制。

## 缓存

- 宏程序：构建缓存（键是编译器、各 crate 的路径和 main.navi，命中要求读过的源文件都没变）。
- 每个请求的回答：键是宏 crate 和它依赖的各 crate 的缓存键（`libcache::key`，内容哈希）、生成的 main 和请求本身（宏名和 `TypeInfo`，含位置），存成缓存目录里的 `<键>.dx`。一个宏 crate 的请求都命中时，宏程序既不编译也不启动。`tests/derive/app`：冷编译 2.38s，回答都在缓存里 0.62s，改了宏 crate 1.36s（重编宏程序）。
- 导出数据：crate 单独编译时，派生生成的文件和它的源文件一起存进 `.navih`（文件名就是生成文件的名字，函数体照常能省则省）。从 `.navih` 读进来的 crate 不再展开，按名字取出这些文件，直接收集里面的条目；派生宏的程序也不用跑。std 有 343 个派生：hello world 热编译的“解析和名字解析”从 100ms（每次重新展开：生成 15.7ms、解析 21.8ms）降到 63ms（拼字符串、每次展开时是 86ms），std 的 `.navih` 从 1.55MB 变成 1.71MB。

## LSP

- 跳到 `#[derive(X)]` 里的 `X`：到派生宏函数（解析时记下每个派生名用的是哪个宏，`resolve::Info.derive_uses`，LSP 的索引把它当作对宏函数的引用，所以查找引用也有）；内置派生没有可跳的地方。
- 悬停在 `X` 上：显示展开后的代码（格式化后；一个条目的几个内置派生是一起生成的，显示的是它们全部）。
- 生成代码里的错误照位置映射显示在用户代码上，在 `#[derive(...)]` 上的附带那行生成的代码。
- 测试：`tests/derive/session.jsonl`。

## 内置派生的迁移（已完成）

八个内置派生都改成生成源码（`compiler/src/derive/mod.navi`），生成的代码和原来 shims 做的事一样：

- `Debug`：单元为 `write_str`，元组为 `debug_tuple(..).field(..).finish()`，具名为 `debug_struct(..).field(name, ..).finish()`；枚举用变体名。
- `Clone`：逐字段 `clone()`。结构体先 `*self` 拷出再解构：经 `&self` 读到的 `&mut` 字段是 `&`，克隆不回去。
- `PartialEq` / `Eq`：同一变体且逐字段相等。
- `PartialOrd` / `Ord`：先比变体序号，再按字段顺序字典序。
- `Hash`：枚举先写变体序号（`isize`），再逐字段 `hash`。
- `Default`：逐字段 `Default::default()`（枚举不能派生）。

"变体序号"是变体在声明里的位置，不是显式的判别值（和原来一样：`HashMap` 的遍历顺序、排序结果都不变）。规则检查（`Eq` 要求 `PartialEq`、`Ord` 要求 `PartialOrd` 和 `Eq` 等）保留在 `types/builtin.navi` 的 `check_derives`。`mir/shims.navi` 里派生的部分、trait 选择里把派生当作已实现的特殊处理都已删除（约 300 行）。

生成的代码走正常的类型检查，所以原来 shims 绕过的约束现在会检查：派生 `Clone` 的类型，字段的类型也必须实现 `Clone`（编译器自己的 `CArg` 因此补上了 `derive(Clone)`）。

测量（编译器自举，Apple M4）：墙钟 1.88–2.02s → 2.10–2.18s（约 +8%），产物 12.63MB → 12.65MB。多出的时间来自生成的代码要解析、检查和走 plan（每个派生方法都是普通函数，plan 要解析其中对字段 trait 方法的调用）。结构体直接访问 `self.field`、枚举比较用嵌套 `match` 而不是拷贝出元组之后，从约 +10% 降到约 +8%。

## 序列化：照搬 serde，去掉运行期反射

`std::serde`（模块路径和名字同 serde；navi 没有生命周期，`'de` 都去掉）：

- `ser`：`Serialize`（`fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error>`）、`Serializer`（`serialize_bool`/`i64`/`str`/`none`/`some`/`unit`/`seq`/`map`/`struct`/`tuple_struct`/各种变体……），以及 `SerializeSeq`、`SerializeMap`、`SerializeStruct`、`SerializeTuple`、`SerializeTupleStruct`、`SerializeTupleVariant`、`SerializeStructVariant`；`ser::Error`。
- `de`：`Deserialize`（`fn deserialize<D: Deserializer>(d: D) -> Result<Self, D::Error>`）、`Deserializer`（`deserialize_any`/`i64`/`str`/`seq`/`map`/`struct`/`enum`/`identifier`/`ignored_any`……）、`Visitor`、`SeqAccess`、`MapAccess`、`EnumAccess`、`VariantAccess`；`de::Error`。
- std 的类型手写实现：整数、浮点、`bool`、`char`、`String`、`&str`（只序列化）、`Option`、`Vec`、数组、元组、`HashMap`、`BTreeMap`、`()`、引用。
- `#[derive(Serialize, Deserialize)]` 是 std 里的派生宏（`std::serde::derive`，原来写在编译器里，2026-11-24 搬出来，同 Rust 的 serde_derive；std 自己的类型不用它们，所以 std 可以定义它们），生成的代码和 serde_derive 的一样，表示方式也一样：结构体是 map，元组结构体是 seq，单字段元组结构体是它的字段，枚举默认外部标记（`"Variant"` 或 `{"Variant": ...}`）。现在 `std::json` 的映射就是这样，所以数据格式不变。
- 属性（`#[serde(...)]`，serde 的常用子集）：`rename`、`rename_all`、`skip`、`skip_serializing_if`、`default`、`deny_unknown_fields`、`tag`/`content`/`untagged`（枚举的内部标记、相邻标记、不带标记；2026-11-24 加上：序列化照 serde_derive，内部标记的 newtype 变体经 `private::TaggedSerializer`，相邻标记的元组和结构体变体经一个引用字段的辅助结构体；反序列化先读成 `private::Content`，再取标记或逐个变体去试），以后按需要再加。
- `std::json`：`to_string`、`to_string_pretty`、`to_writer`、`from_str`、`from_reader`，`T: Serialize` / `T: Deserialize`；`json::Value` 也实现两者（任意 JSON）。
- `std::csv`：按 serde 读写记录（同 Rust 的 csv crate）。
- `std::http` 里 JSON 请求体和响应体的辅助函数加上相应约束。

去掉反射时一起改的：

- `std::reflect`、`HashMap` 的 `ReflectMap`、`Vec` 里给反射用的辅助、`tests/run/reflect.navi` 删除。
- `std::any::type_name::<T>()` 直接读 `type_desc::<T>()` 描述里的名字。
- 编译器生成的类型描述从 12 个字减到 5 个（kind、大小、对齐、名字），也不再为各部分的类型（字段、变体、元素、map 的键和值）生成描述；`ReflectMap` 虚表去掉。描述只在用到 `type_name` 或 `std::unique` 的程序里生成，别的程序不受影响。实测：一个对含 `Vec<HashMap<String, (i32, f64)>>` 字段的结构体调用 `type_name` 的程序，1,149,760 → 1,125,312 字节（−2.1%）；编译器本身 13.50 → 13.44 MB。
- 内置派生给只有一个变体的枚举生成 `PartialEq`、`Ord` 时不再带 `_` 分支（`-d checkstd` 下会报不可达的模式）。
- `TypeInfo::of::<T>()`（宏的测试用）改成编译器内建函数，在编译期构造。
- spec 和 `spec/std-diff.md` 里关于反射的部分改掉。

## 分步计划（每一步都能单独验证和提交）

1. **`std::derive` 的接口和编译器里的展开流程**：`TypeInfo`、`Code` 和带标签的模板 `quote`（解析、插值、位置），按绑定遍历和 `impl_for`，收集之后、检查函数体之前展开，生成的条目加进模块。先用一个内置的测试派生验证。
2. **内置派生改写**：八个派生逐个改成生成源码；每改一个，跑全部测试，并比较改前改后的输出；全部改完后删掉 shims 里的派生和 builtin 里的特殊处理。量 std 的编译时间和编译器自举的产物。自举定点照常检查。
3. **`std::serde`**：traits、std 类型的实现、内置的 `#[derive(Serialize, Deserialize)]` 和 `#[serde(...)]` 属性。
4. **`std::json`、`std::csv`、`std::http` 改用 serde**；测试里的类型加上派生。
5. **去掉运行期反射**：删除 `std::reflect` 和相关辅助，`type_name` 改内建，类型描述瘦身，spec 更新。
6. **用户的派生宏**：`#[derive_macro]`、派生命名空间的名字解析、宏程序的编译和调用、属性命名空间的检查和报错，`FromAttrs`，`navi expand`，`TypeInfo::of`。（已完成，2026-11-24：`std::derive`、`quote` 标签模板、`resolve/expand.navi`、`navi derive-build`、`navi expand`；测试是 `tests/derive`（宏 crate、用它的程序、报错）、`tests/run/derive_api.navi`、`tests/ui/quote.navi`）
7. **缓存和导出数据**：展开结果的缓存键、存进 `.navih`；**LSP**：跳转、悬停显示展开结果、错误位置。（已完成，2026-11-24：回答按请求缓存，不存进 `.navih`，理由见“缓存”）
8. **用例**：`compiler/src/syntax/astcodec.navi` 改成派生，去掉 `tools/gen_astcodec.py`。（已完成：编译器依赖宏 crate `compiler/codec`，ast.navi 的 45 个类型 `#[derive(Codec)]`；写出的 `.navih` 和原来逐字节相同，解码一样快。要求自举用的发布版有派生宏：tools/bootstrap-version 为 v0.0.4）
