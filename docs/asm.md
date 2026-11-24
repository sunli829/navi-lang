# 汇编函数（`#[asm]` 和 `.s` 文件）

照 Go 的做法：navi 代码里只写声明，函数体写在同目录的汇编文件里，由编译器内置的汇编器处理（`compiler/src/asm/`），不需要外部的汇编器或 C 工具链。用途是 CPU 的专用指令（AES、PMULL/PCLMULQDQ 等）和少数手写的热点代码。没有内联汇编：内联汇编要先有向量类型和向量寄存器分配，以及一套新语法；按函数调用，再把整段循环写在汇编里，调用开销可以忽略。

## 写法

```navi
// src/crypto/aes.navi
#[asm]
fn encrypt_blocks(rk: *const u8, rounds: usize, src: *const u8, dst: *mut u8, n: usize);
```

- 汇编写在模块文件旁边，名字是模块文件去掉 `.navi` 加上 `_arm64.s` 或 `_amd64.s`（`aes_arm64.s`、`aes_amd64.s`；`mod.navi` 的是 `mod_arm64.s`）。编译时只读目标架构的那个。
- 文件里每个不以 `.` 开头的标签开始一个函数，名字必须是这个模块的某个 `#[asm] fn`；以 `.` 开头的标签只在它上面那个函数里有效（跳转目标、常量）。
- 被调用到的 `#[asm] fn` 在文件里没有，或者文件里的函数没有声明，都是编译错误。
- `#[asm] fn` 不能是泛型的。它的指针参数在逃逸分析里和 C 函数的一样，算作不逃逸（汇编不能保存 GC 指针）。

## 语法

- arm64 用 GNU 语法：`ldr x0, [sp, #8]`、`ld1 {v0.16b-v3.16b}, [x1], #64`、`aese v0.16b, v1.16b`。
- amd64 用 Intel 语法：`mov rax, qword ptr [rsp + 8]`、`movdqu xmm0, xmmword ptr [rdi]`、`aesenc xmm0, xmm1`；访问常量用 `[rip + .label]`。跳转一律 rel32。
- 注释用 `//`。数据：`.byte`、`.hword`、`.word`、`.quad`；对齐：`.balign n`（字节）或 `.p2align n`。函数的常量放在它的代码后面、下一个函数的标签之前（合并目标文件时按函数切开）。
- 支持的指令：常用的通用指令（传送、算术、逻辑、移位、比较、条件选择、各种寻址方式的读写、跳转），以及 SIMD 和加密指令（arm64：`ld1`/`st1`、`aese`/`aesd`/`aesmc`/`aesimc`、`pmull`/`pmull2`、`ext`、`rev64`、向量移位和逻辑等；amd64：SSE 传送和整数运算、`pshufb`/`pshufd`/`palignr`、AES-NI、`pclmulqdq` 等）。不认识的指令报错，用到时再加。
- 编码的正确性：`tools/asmcheck.py` 把 `tests/asm/<arch>.txt` 里的每条指令分别用这个汇编器和 clang 汇编，再用 objdump 反汇编，结果必须一样（`tools/test.sh` 在 macOS 上跑）。

## 调用约定

和 navi 的函数一样（`compiler/src/backend/lower.navi` 开头）：

| | arm64 | amd64 |
|---|---|---|
| 第 i 个参数（每个 8 字节，结构体按大小向上取整） | 入口时 `[sp, #8*i]` | 入口时 `[rsp + 8 + 8*i]` |
| 结果写到 | `[x8]` | `[r13]` |
| 必须保持 | x18（Apple 保留）、x19–x28（x28 是当前 G）、x29、sp；x30 留到 `ret` | rbx、rbp、r12、r14（当前 G）、rsp |
| 可以随便用 | x0–x17、全部向量寄存器 | 其余通用寄存器、全部 xmm |

C 的入口（回调、程序入口）已经保存了 C 要求保存的向量寄存器，所以汇编函数用哪个向量寄存器都可以。

## 限制

同 Go 的 NOSPLIT 函数：汇编函数是叶子函数，不调用别的函数，不使用自己的栈帧（amd64 可以短暂地 push/pop，但不能改变返回时的 rsp），不分配内存，不往内存里写 GC 指针（没有写屏障）。它没有栈图：执行期间不会被抢占（抢占在函数序言和调用处），GC 不会在它中间扫描它的栈。

## 实现

- `compiler/src/asm/mod.navi`：拆语句、数据指令、按函数切分；`arm64.navi`、`amd64.navi`：两遍（先算每条的大小和标签位置，再编码）。
- 代码生成：调用 `#[asm] fn` 是内建的 `"asm"` 目标，参数放到出参区，RET 指向结果缓冲区，调用它的符号（`asm_sym`，按定义的身份命名，分开编译也一致）。生成目标文件时把本 crate 每个模块的 `.s` 汇编进来，按符号定义函数；没被调用的由合并时去掉。
- 缓存：`.s` 文件算在 crate 的缓存键里（`libcache::key`），也记在构建缓存的文件清单里。
