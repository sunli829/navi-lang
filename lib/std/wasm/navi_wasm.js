// Runs a navi program built for js-wasm32 (docs/wasm.md), as Go's
// wasm_exec.js does Go's: the imports of module "navi" (output, clocks,
// memory, timers) and the run loop's driving (run, then resume after the
// timers it asks for or events).
//
//   const navi = new Navi({ args: ["app"], env: { KEY: "v" } });
//   const { instance } = await WebAssembly.instantiateStreaming(
//     fetch("app.wasm"), navi.importObject());
//   const code = await navi.run(instance);
"use strict";

(() => {
  class NaviExit extends Error {
    constructor(code) {
      super(`exit status ${code}`);
      this.code = code;
    }
  }

  class Navi {
    // opts: args (strings), env ({name: value}), output(fd, text): where
    // standard output (1) and error (2) go (else the console, by lines),
    // imports: more modules (an extern block's #[link(wasm_import_module)]).
    constructor(opts) {
      opts = opts || {};
      this.args = opts.args || [];
      this.env = opts.env || {};
      this.imports = opts.imports || {};
      this.output = opts.output || null;
      this.exited = false;
      this.exitCode = 0;
      this.timer = null;
      this._lines = ["", "", ""];
      this._dec = [null, new TextDecoder(), new TextDecoder()];
      this._done = null;
    }

    _mem() {
      return this.exports.memory.buffer;
    }

    _write(fd, text) {
      if (this.output) {
        this.output(fd, text);
        return;
      }
      // By lines to the console.
      let s = this._lines[fd] + text;
      let i;
      while ((i = s.indexOf("\n")) >= 0) {
        (fd == 2 ? console.error : console.log)(s.slice(0, i));
        s = s.slice(i + 1);
      }
      this._lines[fd] = s;
    }

    importObject() {
      const self = this;
      const navi = {
        // (The compiler's own calls pass 64-bit integers: BigInts.)
        write(fd, p, n) {
          fd = Number(fd);
          p = Number(p);
          n = Number(n);
          const b = new Uint8Array(self._mem(), p, n);
          self._write(fd, self._dec[fd == 2 ? 2 : 1].decode(b, { stream: true }));
          return n;
        },
        exit(code) {
          code = Number(code);
          self.exited = true;
          self.exitCode = code;
          throw new NaviExit(code);
        },
        abort() {
          self.exited = true;
          self.exitCode = 134;
          throw new NaviExit(134);
        },
        nanotime() {
          return Math.floor(performance.now() * 1e6);
        },
        walltime() {
          return Date.now();
        },
        grow(pages) {
          try {
            return self.exports.memory.grow(pages);
          } catch (e) {
            return -1;
          }
        },
        memmove(d, s, n) {
          new Uint8Array(self._mem()).copyWithin(d, s, s + n);
        },
        memset(p, c, n) {
          new Uint8Array(self._mem(), p, n).fill(c);
        },
        floor: Math.floor,
        ceil: Math.ceil,
        trunc: Math.trunc,
        sqrt: Math.sqrt,
        fmod(a, b) {
          return a % b;
        },
        fmodf(a, b) {
          return Math.fround(a % b);
        },
        random(p, n) {
          // (getRandomValues takes at most 65536 bytes at a time.)
          for (let i = 0; i < n; i += 65536) {
            const k = Math.min(65536, n - i);
            crypto.getRandomValues(new Uint8Array(self._mem(), p + i, k));
          }
        },
      };
      return Object.assign({ navi, env: {} }, this.imports);
    }

    // Runs the program; resolves with its exit status.
    run(instance) {
      this.exports = instance.exports;
      const done = new Promise((resolve) => {
        this._done = resolve;
      });
      const [argc, argv] = this._argv();
      this._step(() => this.exports.run(argc, argv));
      return done;
    }

    // The arguments and the environment in memory grown for them, as C's
    // argv and envp (envp after argv's null): [argc, argv].
    _argv() {
      const enc = new TextEncoder();
      const args = this.args.map((s) => enc.encode(s + "\0"));
      const env = Object.entries(this.env).map(([k, v]) => enc.encode(`${k}=${v}\0`));
      const n = args.length + env.length + 2;
      let size = 8 * n;
      for (const b of args.concat(env)) size += b.length;
      const base = this.exports.memory.grow(Math.ceil(size / 65536)) * 65536;
      const dv = new DataView(this._mem());
      let p = base + 8 * n;
      let k = 0;
      const put = (b) => {
        new Uint8Array(this._mem(), p, b.length).set(b);
        dv.setBigUint64(base + 8 * k++, BigInt(p), true);
        p += b.length;
      };
      args.forEach(put);
      dv.setBigUint64(base + 8 * k++, 0n, true);
      env.forEach(put);
      dv.setBigUint64(base + 8 * k++, 0n, true);
      return [args.length, base];
    }

    // Runs f (run or resume): it returns when the program waits (the
    // milliseconds until its next timer, or -1) or exits.
    _step(f) {
      let ms;
      try {
        ms = f();
      } catch (e) {
        this._end(e);
        return;
      }
      if (ms >= 0) {
        this.timer = setTimeout(() => {
          this.timer = null;
          this._step(() => this.exports.resume());
        }, ms);
      }
    }

    _end(e) {
      if (this.timer !== null) {
        clearTimeout(this.timer);
        this.timer = null;
      }
      // What is left of the last lines.
      for (const fd of [1, 2]) {
        const t = this._dec[fd].decode();
        if (t.length > 0) this._write(fd, t);
        if (!this.output && this._lines[fd].length > 0) {
          (fd == 2 ? console.error : console.log)(this._lines[fd]);
          this._lines[fd] = "";
        }
      }
      if (e instanceof NaviExit) {
        this._done(e.code);
        return;
      }
      this.exited = true;
      this.exitCode = 2;
      this._write(2, `navi: ${e && e.stack ? e.stack : e}\n`);
      this._done(2);
    }
  }

  globalThis.Navi = Navi;
})();
