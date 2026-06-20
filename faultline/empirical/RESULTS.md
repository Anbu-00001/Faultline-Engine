# Empirical validation — would Faultline have caught real regressions?

**Headline: on 21 of 32 *analyzable* real-world regressions from [BugsInPy](https://github.com/soarsmu/BugsInPy), changing the buggy symbol reaches code with no test coverage — so Faultline's gate would have fired and named the minimum test to add.**

BugsInPy is a published benchmark of 501 real, reproduced bugs from 17 popular Python projects, each with the exact buggy commit, the fix, and the regression test the fix added. We treat each fix as a merge request and ask Faultline's question: *does the change's transitive blast radius include untested code?*

| Project | Type | Analyzable bugs | Gate would fire | Notes |
|---|---|---|---|---|
| **tornado** | library | 11 | **3** | Most changes are to *public API* methods whose callers are downstream apps, so internal impact is correctly ~0 — Faultline does not cry wolf. |
| **black** | application | 21 | **18** | Internal formatter/parser changes ripple through the codebase; the gate fires and the *minimum test set is usually 1*. |
| **Total** | | **32** | **21** | ~66% of analyzable real regressions reach untested code. |

"Analyzable" = the fix modifies an **existing** function (7 BugsInPy fixes here only *add* a new function — there is no pre-existing symbol to trace, and Orbit would not have indexed the new symbol pre-merge either, so they are excluded, not counted as misses).

## Three verified cases (real call chains, inspect them yourself)

**`black` #10 — a one-character tokenizer fix that silently reaches the whole parse stack.**
The fix changed `_partially_consume_prefix` (whitespace/prefix handling in the tokenizer). Faultline:
- **impacted = 5, all untested:** `parse_tokens`, `parse_string`, `parse_stream`, `parse_stream_raw`, `parse_file`
- **minimum test set = 1:** `parse_tokens` — one test at this choke point gates the entire change.

**`tornado` #11 — a real HTTP correctness regression.**
The fix changed `_read_body`: `headers.get("Transfer-Encoding")` → `headers.get("Transfer-Encoding", "").lower()` (HTTP header values are case-insensitive; the buggy version mishandled `Chunked`). Faultline shows `_read_body` reaches the untested `_read_message` in the HTTP read loop; **minimum test set = 1** (`_read_message`).

**`black` #2 — `fmt: off`/`on` handling.**
The fix changed `generate_ignored_nodes`; it reaches **5 untested** functions in the fmt-off pipeline (`convert_one_fmt_off_pair`, `normalize_fmt_off`, `reformat_one`, …); **minimum test set = 1**.

In every firing case the prescription is small (usually a single test) — the provably-minimal cut from [the engine](../CORRECTNESS.md), now demonstrated against real bugs.

## Honest methodology & scope (what this is and is NOT)

- **Same engine, real binary.** The batch builds the normalized graph JSON, then runs the *exact* `faultline-engine` release binary used by the live gate. The transitive closure, the untested gate, the minimum test set and the Shapley attribution are byte-for-byte the production code. Only the **graph source** differs.
- **Graph source: a conservative static analyzer, not Orbit.** The live gate gets `CALLS` edges from GitLab Orbit. Offline (we cannot import 17 third-party projects into Orbit) we build the graph with a stdlib `ast` analyzer (`faultline_batch.py`) that resolves calls **scope-aware and import-aware**: `self.m()` via the class MRO, bare `f()` to a same-module or imported-and-unique definition, and it **drops** ambiguous dynamic dispatch rather than over-connecting the graph through shared method names. This **under-approximates** (it misses some real edges Orbit would resolve), so these counts are a conservative **lower bound** — the production numbers can only be higher.
- **Coverage is the same name-reference heuristic** the agent uses ("untested" = the symbol's name appears in no test file at the buggy commit), with the same stated limitation (see [CORRECTNESS.md](../CORRECTNESS.md)).
- **No result is asserted.** Every number is computed by the engine from the bug's real buggy commit. Nothing about a bug is hard-coded; point the script at any BugsInPy project to reproduce or extend.
- **What we do NOT claim:** that Faultline *detects bugs*. It flags the **untested-impact condition** through which these real regressions shipped — and names the minimal test the fix's own regression test ended up adding.

See [README.md](README.md) to reproduce. Raw machine output: [`results/`](results/).
