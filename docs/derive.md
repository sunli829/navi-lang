# 派生宏（设计）

## 目标

- 用户可以定义自己的 `#[derive(X)]`，用 navi 写，例如 JSON 序列化、builder、ORM 映射。
- 生成的是普通的 navi 代码，和手写的 impl 一样检查、内联、优化；不靠运行期反射，所以没有逐字段查类型描述的开销。
- 内置的 `Debug`、`Clone`、`PartialEq`、`Eq`、`PartialOrd`、`Ord`、`Hash`、`Default` 也改成派生宏，和用户的走同一套机制。现在 `mir/shims.navi` 里按具体类型现场生成方法体的那部分可以去掉。
- 不做 Rust 那样的函数式宏和任意改写代码的属性宏：只做派生，以及派生宏声明的字段属性。

和 Rust 过程宏的区别：

- 宏拿到的不是 token 流，而是类型检查器收集好的结构：字段、字段类型、泛型、属性，还能查询某个类型是否实现了某个 trait。
- 宏只能生成 impl 块（以及私有的辅助函数），不能定义新类型、不能改写原来的代码。展开一轮就结束，不需要反复迭代。
- 展开结果按输入缓存，并存进导出数据（`.navih`，见 docs/crates.md），下游和重新编译都不用再展开。

## 定义和使用

派生宏是库 crate 里一个带 `#[derive_macro]` 的函数，返回生成的代码：

```navi
use std::derive::{TypeInfo, Shape, Code, Error, quote};

#[derive_macro(Serialize, attrs = [json])]
pub fn serialize(t: &TypeInfo) -> Result<Code, Error> {
    let body = match t.shape() {
        Shape::Struct(fields) => {
            let keys = fields.iter().filter(|f| !f.has_attr("json", "skip")).map(|f| {
                let key = f.attr_str("json", "rename").unwrap_or(f.name());
                quote`w.key(${key}); self.${f.ident()}.serialize(w);`.at(f)
            });
            quote`w.begin_object(); ${keys} w.end_object();`
        }
        Shape::Enum(variants) => { /* 按变体生成 match */ }
        Shape::Unit => quote`w.null();`,
    };
    Ok(quote`
        impl${t.generics()} ::myjson::Serialize for ${t.self_type()} ${t.where_clause("::myjson::Serialize")} {
            fn serialize(&self, w: &mut ::myjson::Writer) { ${body} }
        }
    `)
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

**位置映射到用户代码**（同 Rust `quote!` 保留 span）：

- `Ident`、`TypeRef`、`Field`、`Generics` 等来自用户代码的值，插进去后生成的 token 保留它们在用户源文件里的位置。比如字段类型没有实现 `Serialize`，`self.${f.ident()}.serialize(w)` 的错误指向用户结构体里的那个字段。
- 模板自己的 token 默认位置是用户写的 `#[derive(X)]`，同时记下它在宏源码里的位置，报错时作为附注。
- `` quote`...`.at(x) `` 把整段模板的默认位置改成 `x`（字段、变体、类型）的位置，相当于 Rust 的 `quote_spanned!`：针对整条语句的错误也能落在对应字段上。
- LSP：生成代码里插入的标识符可以跳回用户代码。

**语法提前检查**：编译宏所在的 crate 时，编译器检查每个 `quote` 模板本身能否组成合法的语法片段（括号配对、插值所在的位置），错误在写宏时就报，不用等展开。

**名字解析**：navi 没有卫生宏，生成的代码放在类型所在的模块里解析。宏里引用的外部条目要写完整路径（`::myjson::Serialize`，`::std::fmt::Debug`），和现在内置 derive 的做法一样。

## 宏能看到什么：`std::derive`

- `TypeInfo`：类型名、所在模块路径、可见性、文档注释、类型上的属性；泛型参数和它们的约束（`generics()`、`self_type()`、`where_clause(trait)` 用在 impl 头上）。
- `Shape`：具名或元组结构体（字段列表）、单元结构体、枚举（变体列表，每个变体有名字、判别值、字段）。
- `Field`：名字和 `ident()`（元组字段为 `0`、`1`……）、可见性、类型、属性（`has_attr`、`attr_str` 等）、文档注释、位置。
- `TypeRef`：字段类型。可以拆开（引用、数组、元组、ADT 和它的参数），以及 `implements(path)`：问编译器这个类型是否实现某个 trait。类型上声明了、尚未展开的派生也算实现，所以 `A` 的字段类型 `B` 同样 `#[derive(Serialize)]` 时，问 `B` 得到的是"实现了"。
- `Error`：`Error::at(x, msg)` 在 `x`（字段、类型、属性）的位置报错，例如某个字段的类型不支持。

`TypeInfo` 是纯数据，可以序列化：用户的宏在单独的进程里运行（见下），编译器把它发过去，`Code` 连同各 token 的位置发回来。

## 什么时候展开

在收集完所有类型、签名和 impl 头之后，检查函数体之前：

1. 收集每个类型上的 `#[derive(...)]`，按路径找到派生宏。
2. 对每个（类型，派生）构造 `TypeInfo`，调用宏，得到源码。
3. `Code` 按 token 序列解析成条目（只允许 impl 块和函数；token 带着各自的位置，见上），加进类型所在的模块，然后照常名字解析、收集签名。
4. 之后的函数体检查、MIR、代码生成对它们一视同仁。

生成代码里的错误照常报：位置是出错 token 的位置（来自用户代码的就在用户代码里，模板自己的在 `#[derive(X)]` 上），并附上"由 `derive(Serialize)` 为 `User` 生成"和宏模板里的位置。

## 怎么执行

- **内置派生**写在编译器里（navi 代码，`compiler/src/derive/`），用同一个 `TypeInfo`/`quote` 接口，在编译器进程内直接调用。这样 std 自己的类型可以用它们，不需要先编出 std 再运行宏（自举不成环）。
- **用户的派生宏**：编译器为宿主平台把宏所在的 crate 和一个固定的 main 编成一个小程序（交叉编译时也是宿主平台）。一个 crate 的所有展开请求一次发给它（标准输入里序列化的 `TypeInfo` 列表），收回 `Code`。每个 crate 最多启动一次进程。
- 宏必须是确定的：相同的 `TypeInfo` 得到相同的输出，不读写文件、不看时间和环境。文档里写明，不强制。

## 缓存

- 宏程序：按宏 crate 的缓存键（docs/crates.md）缓存。
- 展开结果：按（宏的键或编译器身份，`TypeInfo` 的哈希）缓存；`TypeInfo` 包含宏能看到的全部输入，包括 `implements` 的答案。
- 导出数据（`.navih`）里存展开后的语法树，下游直接读，不再展开。

## LSP

- 跳到 `#[derive(X)]` 里的 `X`：到派生宏函数（内置的到编译器源码中的说明）。
- 悬停在 `X` 上：显示展开后的代码。
- 生成代码里的错误显示在 `#[derive(...)]` 上，附带生成文件里的位置。

## 内置派生的迁移

- `Debug`：生成 `f.debug_struct(..).field(..).finish()` 等调用（`{:#?}` 的缩进由 Formatter 处理）。
- `Clone`：逐字段 `clone()`。
- `PartialEq` / `Eq`：逐字段比较；枚举先比变体。
- `PartialOrd` / `Ord`：按字段顺序字典序；枚举先比变体序号。
- `Hash`：枚举先写变体序号，再逐字段 `hash`。
- `Default`：逐字段 `Default::default()`（枚举不能派生，和现在一样）。

现在的规则（`Eq` 要求 `PartialEq`、`Ord` 要求 `PartialOrd` 和 `Eq` 等）保留，在展开前检查。迁移后 `mir/shims.navi` 里派生的那部分、`types/builtin.navi` 里把派生当作已实现的特殊处理都去掉，trait 选择看到的是真正的 impl。

要量的：std 里派生很多，展开出的源码每次都要解析和检查（有导出数据时不用）。迁移前后比较 std 的编译时间和编译器自举的产物大小、速度。

## 分步计划（每一步都能单独验证和提交）

1. **`std::derive` 的接口和编译器里的展开流程**：`TypeInfo`、`Code` 和带标签的模板 `quote`（解析、插值、位置），收集之后、检查函数体之前展开，生成的条目加进模块。先用一个内置的测试派生验证。
2. **内置派生改写**：八个派生逐个改成生成源码；每改一个，跑全部测试，并比较改前改后的输出；全部改完后删掉 shims 里的派生和 builtin 里的特殊处理。自举定点照常检查。
3. **用户的派生宏**：`#[derive_macro]`、派生命名空间的名字解析、宏程序的编译和调用、属性命名空间的检查和报错。
4. **缓存和导出数据**：展开结果的缓存键、存进 `.navih`。
5. **LSP**：跳转、悬停显示展开结果、错误位置。
6. **用例**：`compiler/src/syntax/astcodec.navi` 改成 `#[derive(Encode, Decode)]`，去掉 `tools/gen_astcodec.py`；std 里加一个 JSON 的 `Serialize`/`Deserialize` 派生。
