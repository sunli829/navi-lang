# 派生宏（设计）

## 目标

- 用户可以定义自己的 `#[derive(X)]`，用 navi 写，例如 JSON 序列化、builder、ORM 映射。
- 生成的是普通的 navi 代码，和手写的 impl 一样检查、内联、优化；不靠运行期反射，所以没有逐字段查类型描述的开销。
- 内置的 `Debug`、`Clone`、`PartialEq`、`Eq`、`PartialOrd`、`Ord`、`Hash`、`Default` 也改成派生宏，和用户的走同一套机制。现在 `mir/shims.navi` 里按具体类型现场生成方法体的那部分可以去掉。
- 不做 Rust 那样的函数式宏和任意改写代码的属性宏：只做派生，以及派生宏声明的字段属性。
- 去掉运行期反射（`std::reflect`）：`std::json`、`std::csv` 改成照搬 serde 的 `Serialize`/`Deserialize`，用户类型写 `#[derive(Serialize, Deserialize)]`；编译器生成的类型描述只留 GC 需要的部分。

和 Rust 过程宏的区别：

- 宏拿到的不是 token 流，而是类型检查器收集好的结构：字段、字段类型、泛型、属性，还能查询某个类型是否实现了某个 trait。
- 宏只能生成 impl 块（以及私有的辅助函数），不能定义新类型、不能改写原来的代码。展开一轮就结束，不需要反复迭代。
- 展开结果按输入缓存，并存进导出数据（`.navih`，见 docs/crates.md），下游和重新编译都不用再展开。

## 定义和使用

派生宏是库 crate 里一个带 `#[derive_macro]` 的函数，返回生成的代码：

```navi
use std::derive::{TypeInfo, Code, Error, FromAttrs, quote};

// 字段属性 #[json(rename = "...", skip)] 的形状：解析和报错由 FromAttrs 生成。
#[derive(FromAttrs)]
struct JsonField { rename: Option<String>, skip: bool }

#[derive_macro(Serialize, attrs = [json])]
pub fn serialize(t: &TypeInfo) -> Result<Code, Error> {
    // 结构体、元组结构体、枚举一样写：each_variant 生成 match 和每个字段的绑定。
    let body = t.each_variant(|v| {
        let fields = v.bindings().try_filter_map(|b| {
            let a: JsonField = b.field().attrs("json")?;
            if a.skip { return Ok(None); }
            let key = a.rename.unwrap_or(b.field().name());
            Ok(Some(quote`w.key(${key}); ${b}.serialize(w);`))
        })?;
        Ok(quote`w.begin_object(); ${fields} w.end_object();`)
    })?;
    let method = quote`fn serialize(&self, w: &mut ::myjson::Writer) { ${body} }`;
    // impl 头（泛型参数、给类型参数加的约束）由 impl_for 生成。
    Ok(t.impl_for("::myjson::Serialize", method))
}
```

- `#[derive_macro(Name, attrs = [ns, ...])]`：`Name` 是 `#[derive(...)]` 里写的名字；`attrs` 是这个宏认领的字段和类型属性的命名空间（`#[json(...)]`）。没有被任何派生认领的属性报错，拼错的属性不会被悄悄忽略。
- 使用：派生名和普通条目一样经 `use` 引入（`use myjson::Serialize;`），然后 `#[derive(Serialize)]`。没有全局注册表；同名的 trait 和派生宏可以从同一个路径引入（类型命名空间里的 trait，派生命名空间里的宏）。
- 内置派生在 std 的 prelude 里，写法不变。

## 生成代码：`quote`

`` quote`...` `` 是带标签的模板（JS 的 tagged template）：普通函数 `std::derive::quote` 的调用写法，不是宏。模板可以跨行；它生成的是 token 序列（`Code`），不是文本，所以模板里的缩进和换行不影响结果。

插值按值的类型变成代码（`ToCode` trait），不靠字符串拼接：

| 插入的值 | 生成的代码 |
|---|---|
| `String`、`&str` | 字符串字面量，自动转义（`${key}` 生成 `"id"`） |
| `Ident` | 标识符（`self.${f.ident()}`） |
| `TypeRef`、`Generics`、`WhereClause`、`Path` | 对应的类型、泛型参数、约束、路径 |
| 整数、`bool`、`char` | 字面量 |
| `Code` | 原样嵌入 |
| 迭代器、`Vec<T: ToCode>` | 依次展开（相当于 Rust `quote!` 的 `#(...)*`） |

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
- 模板自己的 token 默认位置是用户写的 `#[derive(X)]`，同时记下它在宏源码里的位置，报错时作为附注。
- `` quote`...`.at(x) `` 把整段模板的默认位置改成 `x`（字段、变体、类型）的位置，相当于 Rust 的 `quote_spanned!`：针对整条语句的错误也能落在对应字段上。
- LSP：生成代码里插入的标识符可以跳回用户代码。

**语法提前检查**：编译宏所在的 crate 时，编译器检查每个 `quote` 模板本身能否组成合法的语法片段（括号配对、插值所在的位置），错误在写宏时就报，不用等展开。

**名字解析**：navi 没有卫生宏，生成的代码放在类型所在的模块里解析。宏里引用的外部条目要写完整路径（`::myjson::Serialize`，`::std::fmt::Debug`），和现在内置 derive 的做法一样。

## 写宏的辅助

写派生宏的时间大多不花在拼代码上，而在下面几件事上（Rust 里分别靠 synstructure、darling 和 cargo expand），这里都内置：

- **按绑定遍历**：结构体、元组结构体、枚举不用分别处理，也不用手写解构模式。
  - `t.each(|b| ...)`：生成 `match self { 模式 => { 每个绑定的代码 } ... }`，`b` 插入时是那个字段的绑定。
  - `t.each_variant(|v| ...)`：按变体生成，`v.bindings()` 是它的字段绑定，`v.name()` 是变体名。
  - `t.each_construct(|b| ...)`：生成构造同一变体的表达式（`Clone`、`Default` 这类）。
  - 两个值一起比较时（`PartialEq`、`Ord`），`t.each_pair(other, |a, b| ...)` 同时解构两边。
- **impl 头**：`t.impl_for("::path::Trait", items)`（trait 的路径写成字符串，编译器解析并检查）生成 `impl<...> trait for Self<...> where ...`，类型参数自动加上这个 trait 的约束（同 Rust 的 derive）；`impl_for_with(trait, bounds, items)` 自己给约束。
- **属性解析**：`#[derive(FromAttrs)]` 描述一个属性的形状（`Option<T>` 是可选的键，`bool` 是标志，其余必填），`f.attrs::<T>("json")` 解析成它；未知的键、缺少的键、类型不对都自动报错，位置在属性上。`FromAttrs` 本身是内置派生。
- **查看展开结果**：`navi expand <crate> [类型]` 打印派生展开后的代码（格式化后），相当于 `cargo expand`。
- **测试**：宏可以在普通的 `#[test]` 里调用：`TypeInfo::of::<T>()` 由编译器内建函数在编译期构造，`code.to_string()` 得到格式化后的文本用来比较。

## 宏能看到什么：`std::derive`

- `TypeInfo`：类型名、所在模块路径、可见性、文档注释、类型上的属性；泛型参数和它们的约束（`generics()`、`self_type()`、`where_clause(trait)` 用在 impl 头上）。
- `Shape`：具名或元组结构体（字段列表）、单元结构体、枚举（变体列表，每个变体有名字、判别值、字段）。
- `Field`：名字和 `ident()`（元组字段为 `0`、`1`……）、可见性、类型、属性（`has_attr`、`attr_str` 等）、文档注释、位置。
- `TypeRef`：字段类型，按写出来的语法：可以拆开（路径和它的泛型参数、引用、数组、元组），插进 `quote` 就是这个类型。
- `Error`：`Error::at(x, msg)` 在 `x`（字段、类型、属性）的位置报错，例如某个字段的类型不支持。

`TypeInfo` 是纯数据，可以序列化：用户的宏在单独的进程里运行（见下），编译器把它发过去，`Code` 连同各 token 的位置发回来。

## 什么时候展开

和 Rust 一样，按语法展开：crate 的文件解析完、收集条目时（`resolve::load`），名字解析之前。

1. 收集到一个结构体或枚举时，看它的 `#[derive(...)]`，按名字找到派生宏。
2. 调用宏，得到生成的代码。
3. 生成的代码作为一个文件解析成条目，只允许 impl 块和函数。文件名是原文件名加上 `[derive X for T@位置]`，所以生成条目的稳定符号名（symbols::def_id）不会和别的类型的重复，crate 换了位置也不变。
4. 条目收集进类型所在的模块，之后的名字解析、收集签名、检查函数体、MIR、代码生成对它们一视同仁。

宏看到的是语法：字段的类型是写出来的样子（结构化的，可以拆开路径、泛型参数、引用、数组、元组），不是类型检查后的类型。所以 `implements` 这类需要类型信息的查询暂时没有；以后需要时，作为类型收集之后的第二阶段再加。

生成代码里的错误照常报，位置在生成的文件里，文件名指明是哪个类型的哪个派生。

## 怎么执行

- **内置派生**写在编译器里（`compiler/src/derive/`），在编译器进程内直接调用。这样 std 自己的类型可以用它们，不需要先编出 std 再运行宏（自举不成环）。编译器的源码要能被上一个发布版编译（docs/crates.md），在发布带 `quote` 的版本之前不能用 `quote`，所以内置派生直接拼生成的源码；发布之后改成 `quote`。
- **用户的派生宏**：编译器为宿主平台把宏所在的 crate 和一个固定的 main 编成一个小程序（交叉编译时也是宿主平台）。一个 crate 的所有展开请求一次发给它（标准输入里序列化的 `TypeInfo` 列表），收回 `Code`。每个 crate 最多启动一次进程。
- 宏必须是确定的：相同的 `TypeInfo` 得到相同的输出，不读写文件、不看时间和环境。文档里写明，不强制。

## 缓存

- 宏程序：按宏 crate 的缓存键（docs/crates.md）缓存。
- 展开只看条目自己的语法，所以按 crate 缓存就够了：crate 的缓存键（内容哈希）不变，展开结果就不变。从导出数据（`.navih`）读进来的 crate 再展开一次（很快：只是生成和解析 impl），不另做缓存。

## LSP

- 跳到 `#[derive(X)]` 里的 `X`：到派生宏函数（内置的到编译器源码中的说明）。
- 悬停在 `X` 上：显示展开后的代码。
- 生成代码里的错误显示在 `#[derive(...)]` 上，附带生成文件里的位置。

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
- `#[derive(Serialize, Deserialize)]` 是内置派生（和 `Debug` 一样写在编译器里），生成的代码和 serde_derive 的一样，表示方式也一样：结构体是 map，元组结构体是 seq，单字段元组结构体是它的字段，枚举默认外部标记（`"Variant"` 或 `{"Variant": ...}`）。现在 `std::json` 的映射就是这样，所以数据格式不变。
- 属性（`#[serde(...)]`，serde 的常用子集）：`rename`、`rename_all`、`skip`、`skip_serializing_if`、`default`、`deny_unknown_fields`、`tag`/`content`/`untagged`（枚举表示），以后按需要再加。
- `std::json`：`to_string`、`to_string_pretty`、`to_writer`、`from_str`、`from_reader`，`T: Serialize` / `T: Deserialize`；`json::Value` 也实现两者（任意 JSON）。
- `std::csv`：按 serde 读写记录（同 Rust 的 csv crate）。
- `std::http` 里 JSON 请求体和响应体的辅助函数加上相应约束。

去掉反射时一起改的：

- `std::reflect`、`HashMap` 的 `ReflectMap`、`Vec` 里给反射用的辅助、`tests/run/reflect.navi` 删除。
- `std::any::type_name::<T>()` 直接读 `type_desc::<T>()` 描述里的名字。
- 编译器生成的类型描述从 12 个字减到 5 个（kind、大小、对齐、名字），也不再为各部分的类型（字段、变体、元素、map 的键和值）生成描述；`ReflectMap` 虚表去掉。描述只在用到 `type_name` 或 `std::unique` 的程序里生成，别的程序不受影响。实测：一个对含 `Vec<HashMap<String, (i32, f64)>>` 字段的结构体调用 `type_name` 的程序，1,149,760 → 1,125,312 字节（−2.1%）；编译器本身 13.50 → 13.44 MB。
- 内置派生给只有一个变体的枚举生成 `PartialEq`、`Ord` 时不再带 `_` 分支（`NAVI_CHECK_STD` 下会报不可达的模式）。
- `TypeInfo::of::<T>()`（宏的测试用）改成编译器内建函数，在编译期构造。
- spec 和 `spec/std-diff.md` 里关于反射的部分改掉。

## 分步计划（每一步都能单独验证和提交）

1. **`std::derive` 的接口和编译器里的展开流程**：`TypeInfo`、`Code` 和带标签的模板 `quote`（解析、插值、位置），按绑定遍历和 `impl_for`，收集之后、检查函数体之前展开，生成的条目加进模块。先用一个内置的测试派生验证。
2. **内置派生改写**：八个派生逐个改成生成源码；每改一个，跑全部测试，并比较改前改后的输出；全部改完后删掉 shims 里的派生和 builtin 里的特殊处理。量 std 的编译时间和编译器自举的产物。自举定点照常检查。
3. **`std::serde`**：traits、std 类型的实现、内置的 `#[derive(Serialize, Deserialize)]` 和 `#[serde(...)]` 属性。
4. **`std::json`、`std::csv`、`std::http` 改用 serde**；测试里的类型加上派生。
5. **去掉运行期反射**：删除 `std::reflect` 和相关辅助，`type_name` 改内建，类型描述瘦身，spec 更新。
6. **用户的派生宏**：`#[derive_macro]`、派生命名空间的名字解析、宏程序的编译和调用、属性命名空间的检查和报错，`FromAttrs`，`navi expand`，`TypeInfo::of`。
7. **缓存和导出数据**：展开结果的缓存键、存进 `.navih`；**LSP**：跳转、悬停显示展开结果、错误位置。
8. **用例**：`compiler/src/syntax/astcodec.navi` 改成派生，去掉 `tools/gen_astcodec.py`。
