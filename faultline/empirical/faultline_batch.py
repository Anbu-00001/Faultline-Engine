#!/usr/bin/env python3
"""Faultline empirical batch over BugsInPy real-world regressions.

For each bug we check out the project at its *buggy* commit, build a CALLS graph by
static analysis, map the fix's changed lines to the enclosing definitions (the same
diff->symbol line-range overlap the production agent uses on Orbit data), determine
coverage with the same name-reference heuristic the agent uses, and run the SAME
faultline-engine binary. We then record whether Faultline's gate would have fired —
i.e. whether the change's transitive blast radius included untested code.

Honest scope (see RESULTS.md): the offline batch uses a stdlib `ast` static analyzer
as the CALLS source; the live GitLab gate uses Orbit for the same edges. The engine —
the part that computes the transitive closure, the untested gate, the minimum test set
and the Shapley attribution — is byte-for-byte identical in both. Call resolution is
scope-aware and emits an edge only when the target is unambiguous (see build_graph), so
it UNDER-approximates: ambiguous dynamic dispatch is dropped rather than over-connecting
the graph through shared method names. The reported blast radius is therefore a
conservative lower bound — real impact can only be larger, which makes the empirical
fire-rate a floor, not a ceiling. No bug result is asserted; every number is computed
by the engine.

Dependencies: Python 3.8+ stdlib only. Requires a built faultline-engine and local
clones of BugsInPy and the target project (paths passed as args).
"""
import argparse
import ast
import json
import os
import re
import subprocess
import tempfile
from collections import defaultdict


def is_test_path(path: str) -> bool:
    """Mirror of the agent's isTestFile: test code is for coverage, not the graph."""
    b = os.path.basename(path)
    if b.startswith("test_"):
        return True
    if any(b.endswith(s) for s in ("_test.py", "_tests.py", "_spec.py")):
        return True
    return any(seg in path for seg in ("/test/", "/tests/", "/spec/", "/__tests__/"))


def read_info(bug_dir: str) -> dict:
    info = {}
    with open(os.path.join(bug_dir, "bug.info")) as f:
        for line in f:
            line = line.strip()
            m = re.match(r'^(\w+)="(.*)"$', line)
            if m:
                info[m.group(1)] = m.group(2)
    return info


def _direct_calls(node):
    """ast.Call nodes inside a function body, NOT descending into nested defs (whose
    calls belong to those nested defs, not this one)."""
    out = []
    for child in ast.iter_child_nodes(node):
        if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        if isinstance(child, ast.Call):
            out.append(child)
        out.extend(_direct_calls(child))
    return out


def _base_names(node):
    """Local base-class names of a ClassDef (ignoring external/dotted bases we can't resolve)."""
    out = []
    for b in node.bases:
        if isinstance(b, ast.Name):
            out.append(b.id)
        elif isinstance(b, ast.Attribute):
            out.append(b.attr)
    return out


class _Collector(ast.NodeVisitor):
    """Collect function defs (with enclosing class) and class hierarchy, for scope-aware,
    inheritance-aware resolution."""

    def __init__(self, rel):
        self.rel = rel
        self.class_stack = []
        self.defs = []     # (id, node, class_name|None)
        self.classes = []  # (class_name, [base_names])

    def visit_ClassDef(self, node):
        self.classes.append((node.name, _base_names(node)))
        self.class_stack.append(node.name)
        self.generic_visit(node)
        self.class_stack.pop()

    def _func(self, node):
        cls = self.class_stack[-1] if self.class_stack else None
        nid = f"{self.rel}:{node.lineno}:{node.name}"
        self.defs.append((nid, node, cls))
        self.generic_visit(node)

    visit_FunctionDef = _func
    visit_AsyncFunctionDef = _func


def build_graph(src_dir: str):
    """Return (nodes, edges, defs_by_file, test_corpus) with SCOPE-AWARE call resolution.

    A call resolves to an edge only when the target is unambiguous:
      - self/cls.m()  -> method m on the SAME class (else the unique project-wide m).
      - bare f()      -> a top-level def f in the SAME module (else the unique project f).
      - obj.m()       -> only the unique project-wide def named m (else dropped).
    This deliberately drops ambiguous dynamic dispatch rather than over-connecting the
    graph through shared method names (get/close/write), keeping the blast radius
    credible. It under-approximates (misses some inherited/dynamic edges) — the same
    "known call paths only" boundary the production gate states (see CORRECTNESS.md).
    """
    nodes = []
    defs_by_file = defaultdict(list)
    func_nodes = []  # (id, node, rel, class_name|None)
    name_global = defaultdict(list)        # name -> [id]            (whole project)
    module_funcs = defaultdict(dict)       # rel  -> {name: id}      (module top-level)
    class_methods = defaultdict(dict)      # (rel, cls) -> {name: id}
    class_bases = {}                       # (rel, cls) -> [base_names]
    class_by_name = defaultdict(list)      # cls_name -> [(rel, cls)]
    imported_names = defaultdict(set)      # rel -> {names imported into this module}
    wildcard_modules = set()               # rel that does `from x import *`
    test_corpus_parts = []

    for root, dirs, files in os.walk(src_dir):
        dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "vendor", "__pycache__")]
        for fn in files:
            if not fn.endswith(".py"):
                continue
            full = os.path.join(root, fn)
            rel = os.path.relpath(full, src_dir)
            try:
                with open(full, encoding="utf-8", errors="ignore") as fh:
                    text = fh.read()
            except OSError:
                continue
            if is_test_path(rel):
                test_corpus_parts.append(text)
                continue
            try:
                tree = ast.parse(text)
            except SyntaxError:
                continue  # skip files this Python can't parse (old py2 syntax, etc.)
            col = _Collector(rel)
            col.visit(tree)
            for nid, node, cls in col.defs:
                end = getattr(node, "end_lineno", node.lineno) or node.lineno
                nodes.append({"id": nid, "name": node.name, "file_path": rel,
                              "definition_type": "Function"})
                defs_by_file[rel].append((node.lineno, end, nid))
                func_nodes.append((nid, node, rel, cls))
                name_global[node.name].append(nid)
                if cls is None:
                    module_funcs[rel][node.name] = nid
                else:
                    class_methods[(rel, cls)][node.name] = nid
            for cname, bases in col.classes:
                class_bases[(rel, cname)] = bases
                class_by_name[cname].append((rel, cname))
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    for a in node.names:
                        imported_names[rel].add((a.asname or a.name).split(".")[0])
                elif isinstance(node, ast.ImportFrom):
                    for a in node.names:
                        if a.name == "*":
                            wildcard_modules.add(rel)
                        else:
                            imported_names[rel].add(a.asname or a.name)

    def lookup_method(rel, cls, attr, seen=None):
        """Resolve self/cls.attr() to a method via the class's local MRO (own methods,
        then local base classes), so inherited calls resolve without name explosion."""
        if cls is None:
            return None
        seen = seen if seen is not None else set()
        key = (rel, cls)
        if key in seen:
            return None
        seen.add(key)
        if key not in class_bases:  # base defined in another module: resolve by unique name
            keys = class_by_name.get(cls, [])
            if len(keys) != 1:
                return None
            key = keys[0]
            rel = key[0]
        if attr in class_methods.get(key, {}):
            return class_methods[key][attr]
        for b in class_bases.get(key, ()):
            r = lookup_method(rel, b, attr, seen)
            if r:
                return r
        return None

    def unique_global(name, rel):
        """A cross-module target only if the name is actually imported here (no false
        bridges between unrelated modules) and resolves to exactly one definition."""
        if name in imported_names.get(rel, ()) or rel in wildcard_modules:
            ids = name_global.get(name, ())
            return ids[0] if len(ids) == 1 else None
        return None

    def resolve(call, rel, cls):
        f = call.func
        if isinstance(f, ast.Attribute):
            attr = f.attr
            recv = f.value
            if isinstance(recv, ast.Name) and recv.id in ("self", "cls"):
                r = lookup_method(rel, cls, attr)
                if r:
                    return r
            return unique_global(attr, rel)  # obj.m(): only if imported + unique
        if isinstance(f, ast.Name):
            if f.id in module_funcs.get(rel, {}):
                return module_funcs[rel][f.id]
            return unique_global(f.id, rel)
        return None

    edge_set = set()
    for nid, node, rel, cls in func_nodes:
        for call in _direct_calls(node):
            tid = resolve(call, rel, cls)
            if tid and tid != nid:
                edge_set.add((nid, tid))  # nid CALLS tid
    edges = [{"type": "CALLS", "from": a, "to": b} for (a, b) in sorted(edge_set)]
    return nodes, edges, defs_by_file, "\n".join(test_corpus_parts)


def changed_def_ids(patch_path: str, defs_by_file: dict) -> list:
    """Map the fix's changed (buggy-side) lines to enclosing definitions, the same
    line-range overlap the agent uses. Source files only — a fix's new test is not a
    'changed symbol'."""
    with open(patch_path, encoding="utf-8", errors="ignore") as f:
        patch = f.read()
    changed = set()
    cur_file = None
    bline = 0
    for line in patch.splitlines():
        if line.startswith("--- a/"):
            continue
        if line.startswith("+++ b/"):
            cur_file = line[6:].strip()
            continue
        if line.startswith("@@"):
            m = re.search(r"@@ -(\d+)", line)
            bline = int(m.group(1)) if m else 0
            continue
        if cur_file is None or is_test_path(cur_file):
            continue
        if line.startswith("-") and not line.startswith("---"):
            # a removed (changed) buggy-side line -> map to its enclosing def
            for (start, end, nid) in defs_by_file.get(cur_file, ()):
                if start <= bline <= end:
                    changed.add(nid)
            bline += 1
        elif line.startswith("+") and not line.startswith("+++"):
            pass  # additions don't advance the buggy-side counter
        else:
            bline += 1
    return sorted(changed)


def tested_def_ids(nodes: list, test_corpus: str) -> list:
    """A def is 'tested' if its name appears (word-boundary) in any test file — the
    exact name-reference heuristic the production agent uses for coverage."""
    ids = []
    for n in nodes:
        if n["name"] and re.search(r"\b" + re.escape(n["name"]) + r"\b", test_corpus):
            ids.append(n["id"])
    return ids


def run_engine(engine: str, nodes, edges, changed, tested) -> dict:
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tf:
        json.dump({"nodes": nodes, "edges": edges}, tf)
        graph_path = tf.name
    try:
        out = subprocess.check_output(
            [engine, "--graph", graph_path, "--changed", ",".join(changed),
             "--tested", ",".join(tested)],
            stderr=subprocess.PIPE)
    finally:
        os.unlink(graph_path)
    return json.loads(out)


def git_checkout(src_dir: str, commit: str):
    subprocess.run(["git", "-C", src_dir, "checkout", "-q", "-f", commit],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bugsinpy", required=True, help="path to a BugsInPy checkout")
    ap.add_argument("--project", required=True, help="project name, e.g. tornado")
    ap.add_argument("--project-src", required=True, help="git clone of the project")
    ap.add_argument("--engine", required=True, help="path to faultline-engine")
    ap.add_argument("--bugs", default="", help="comma-separated bug ids (default: all)")
    ap.add_argument("--out", default="", help="optional path to write results JSON")
    args = ap.parse_args()

    bugs_root = os.path.join(args.bugsinpy, "projects", args.project, "bugs")
    if args.bugs:
        bug_ids = [b.strip() for b in args.bugs.split(",") if b.strip()]
    else:
        bug_ids = sorted((d for d in os.listdir(bugs_root) if d.isdigit()), key=int)

    results = []
    print(f"Faultline empirical batch — project={args.project}, {len(bug_ids)} bug(s)\n")
    for bid in bug_ids:
        bug_dir = os.path.join(bugs_root, bid)
        try:
            info = read_info(bug_dir)
            git_checkout(args.project_src, info["buggy_commit_id"])
            nodes, edges, defs_by_file, corpus = build_graph(args.project_src)
            changed = changed_def_ids(os.path.join(bug_dir, "bug_patch.txt"), defs_by_file)
            if not changed:
                results.append({"bug": bid, "status": "no-existing-symbol-changed",
                                "changed": [], "impacted": 0, "untested": 0, "gate": False})
                print(f"  {args.project}/{bid:>3}: fix adds new code only (no existing symbol changed) — n/a")
                continue
            tested = tested_def_ids(nodes, corpus)
            tested_set = set(tested)
            rep = run_engine(args.engine, nodes, edges, changed, tested)
            gate = rep["untested_count"] > 0
            changed_names = sorted({c.split(":")[-1] for c in changed})
            untested_names = sorted({n["name"] for n in rep["blast_radius"]
                                     if n["id"] not in tested_set})
            minset_names = sorted({t["name"] for t in rep.get("minimum_test_set", [])})
            results.append({
                "bug": bid, "status": "ok", "changed": changed_names,
                "impacted": rep["impacted_count"], "untested": rep["untested_count"],
                "min_test_set": len(rep.get("minimum_test_set", [])),
                "minset_names": minset_names, "untested_names": untested_names,
                "max_depth": rep["max_depth"], "gate": gate,
            })
            flag = "GATE FIRES" if gate else "clean"
            print(f"  {args.project}/{bid:>3}: changed {','.join(changed_names)[:40]:<40} "
                  f"impacted={rep['impacted_count']:>3} untested={rep['untested_count']:>3} "
                  f"minset={len(rep.get('minimum_test_set', []))}  -> {flag}")
            if len(bug_ids) == 1:  # single-bug detail mode
                print(f"      untested impacted: {', '.join(untested_names)}")
                print(f"      minimum test set : {', '.join(minset_names)}")
        except Exception as e:  # keep the batch going; record the failure honestly
            results.append({"bug": bid, "status": f"error: {e}", "gate": False})
            print(f"  {args.project}/{bid:>3}: ERROR {e}")

    ok = [r for r in results if r["status"] == "ok"]
    fired = [r for r in ok if r["gate"]]
    print(f"\nSummary: {len(fired)}/{len(ok)} analyzable bugs would FIRE the gate "
          f"(untested impact); {len(results) - len(ok)} not analyzable.")
    if args.out:
        with open(args.out, "w") as f:
            json.dump({"project": args.project, "results": results,
                       "fired": len(fired), "analyzable": len(ok)}, f, indent=2)
        print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
