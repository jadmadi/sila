# Credits & Acknowledgments

`sila` (صِلَة) stands on the shoulders of pioneering research, agent-first design standards, and robust open-source foundations. We gratefully acknowledge the following individuals, specifications, and projects.

---

## 1. Agent Ergonomics & Architecture Standards

### **Kun Chen ([@kunchenguid](https://x.com/kunchenguid))**
* **AXI (Agent eXperience Interface)**: [github.com/kunchenguid/axi](https://github.com/kunchenguid/axi) · [axi.md](https://axi.md)  
  * **License**: MIT
  * **Impact**: Ten foundational design principles for building agent-ergonomic CLIs that treat token budget, deterministic state, and turn count as first-class constraints. Sila adopts AXI principles across its CLI surface: content-first execution, minimal default schemas, definitive empty states, structured in-stream errors, and contextual next-step disclosure.
* **Backpass (Gradient Descent on AGENTS.md)**: [github.com/kunchenguid/backpass](https://github.com/kunchenguid/backpass)  
  * **License**: MIT
  * **Impact**: Pioneering framework conceptualizing instruction optimization as gradient descent: `AGENTS.md` as model weights, multi-agent session transcripts as empirical loss signal, and backpropagation on prompt text. Demonstrates strict token budgeting (~1,000–1,500 token ceiling), empirical error logging with verbatim quote floors, and skills as overflow valves to combat context dilution.
* **First Mate (Crew Orchestrator & Zero-Token Supervision)**: [github.com/kunchenguid/firstmate](https://github.com/kunchenguid/firstmate)  
  * **License**: MIT
  * **Impact**: Pioneering multi-agent orchestration architecture running autonomous tmux worker sessions paired with isolated disposable Git worktrees and zero-token deterministic state supervision. Directly inspired `sila crew` and MCP tools `spawn_crewmate` and `supervise_crew`.
* **Worktree & Sandbox Patterns (Treehouse & No-Mistakes)**: [github.com/kunchenguid/treehouse](https://github.com/kunchenguid/treehouse) · [github.com/kunchenguid/no-mistakes](https://github.com/kunchenguid/no-mistakes)  
  * **License**: MIT
  * **Impact**: Go Git worktree pooling and pre-push verification pipeline in isolated worktrees. Sila natively adapts this architecture in `sila worktree`, `sila gate install [--hook|--remote]`, `sila gate pipeline`, and MCP tool `run_gate_pipeline`.
---

## 2. Serialization & Protocol Specifications

### **[TOON (Token-Oriented Object Notation)](https://toonformat.dev/)**
* **Specification & Reference Implementation**: [toonformat.dev](https://toonformat.dev/) · [github.com/toon-format](https://github.com/toon-format)
* **License**: MIT
* **Impact**: Provides ~40% token savings over JSON while maintaining strict structure, schema transparency, and seamless LLM readability. Sila implements `--toon` across `search`, `prime`, `dream query`, `opinions`, and gate reports.

### **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/)**
* **Specification & Reference**: [modelcontextprotocol.io](https://modelcontextprotocol.io/) · [Anthropic](https://anthropic.com)
* **License**: MIT
* **Impact**: Open protocol standard for seamless agent-to-environment tool execution, powering Sila's 58-tool stdio server for Claude, Cursor, Antigravity, Devin, and any MCP-compliant client.

---

## 3. Storage & Systems Foundations (Go Dependencies)

### **Pure-Go SQLite ([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite))**
* **Author**: Jan Michálek (cznic / modernc.org)
* **License**: BSD-3-Clause
* **Impact**: 100% pure Go CGO-free transpiled SQLite with FTS5, enabling zero-dependency standalone binary distribution (`CGO_ENABLED=0`) and reliable concurrent transactions.

### **go-isatty ([github.com/mattn/go-isatty](https://github.com/mattn/go-isatty))**
* **Author**: Yasuhiro Matsumoto (mattn)
* **License**: MIT
* **Impact**: Terminal and TTY detection ensuring ANSI color codes are rendered cleanly on interactive terminals and stripped in headless CI pipelines or redirected streams.

### **golang.org/x/sys ([pkg.go.dev/golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys))**
* **Author**: The Go Authors
* **License**: BSD-3-Clause
* **Impact**: Low-level operating system primitives, terminal raw mode handling, and process control across Linux, macOS, and Windows.

### **Sibyl Labs & Sibyl-Memory ([sibyllabs.org](https://sibyllabs.org/))**
* **Hierarchical Intent Tiers & LongMemEval Validation**: [github.com/Sibyl-Labs/Sibyl-Memory](https://github.com/Sibyl-Labs/Sibyl-Memory)
* **License**: MIT
* **Impact**: Demonstrated on LongMemEval Oracle (ICLR 2025 benchmark, #2 globally at 95.6%) that local-first, file-based SQLite with FTS5 and zero vector embeddings outperforms complex vector databases for AI agent long-term memory. Sibyl's tiering model reinforces Sila's vectorless, pure-SQLite knowledge substrate.

---

## 4. Architecture Decision Records

### **MADR (Markdown Architectural Decision Records)**
* **Authors**: Oliver Kopp & the MADR Community ([adr.github.io/madr](https://adr.github.io/madr/))
* **License**: MIT
* **Impact**: Clean, structured architectural decision template format powering `sila adr new`, `sila adr compile`, and disk-drift verification.

---

## 5. License Compatibility & Independence Guarantee

Sila is an independent, pure-Go implementation. All compiled Go dependencies (direct and transitive) and referenced open-source specifications are licensed strictly under permissive terms (**MIT** or **BSD-3-Clause**):
* **Zero Non-Commercial Contamination**: No PolyForm, Commons Clause, or non-commercial restrictions exist anywhere in Sila's dependency tree or codebase.
* **100% AGPL v3 & Commercial Dual-Licensing Compatibility**: All dependencies allow unconstrained commercial use, modification, and redistribution without imposing reciprocal copyleft or proprietary restrictions.
* **Log Ingestion Isolation**: Compatibility with third-party coding agent session logs (Claude Code, Antigravity, Devin, OpenCode, Codex, MiMo, Command Code, CodeWhale, ZCode, Muse) is achieved entirely through custom, read-only log parsers written in pure Go. Sila does not bundle, link, or redistribute any third-party agent binaries or proprietary code.

---

## 6. Author & Support

`sila` is conceived and built by **Jad Madi**:
* **Blog**: [jadmadi.net](https://jadmadi.net)
* **X.com**: [@jadmadi](https://x.com/jadmadi)
* **GitHub**: [@jadmadi](https://github.com/jadmadi)
* **Support my work**: [paypal.me/madise](https://paypal.me/madise)
