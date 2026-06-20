# Empirical batch — reproduce it

`faultline_batch.py` runs Faultline against real reproduced bugs from
[BugsInPy](https://github.com/soarsmu/BugsInPy) and reports, per bug, whether the
change's transitive blast radius includes untested code (i.e. whether the gate would
fire). Results and honest caveats: [RESULTS.md](RESULTS.md).

**Dependencies:** Python 3.8+ (stdlib only — no pip installs), a built
`faultline-engine`, and local clones of BugsInPy and the target project.

```bash
# 1. build the engine
( cd ../engine && cargo build --release )

# 2. get the benchmark + a target project
git clone --depth 1 https://github.com/soarsmu/BugsInPy /tmp/BugsInPy
git clone https://github.com/psf/black            /tmp/black     # full history (needs old commits)
git clone https://github.com/tornadoweb/tornado   /tmp/tornado

# 3. run the batch (it checks out each bug's buggy commit itself)
python3 faultline_batch.py \
  --bugsinpy /tmp/BugsInPy --project black --project-src /tmp/black \
  --engine ../engine/target/release/faultline-engine --out results/black-results.json

# single bug, with the impacted/untested/min-test-set detail:
python3 faultline_batch.py --bugsinpy /tmp/BugsInPy --project black \
  --project-src /tmp/black --engine ../engine/target/release/faultline-engine --bugs 10
```

## How it integrates with the rest of Faultline

It reuses the production pieces unchanged, swapping only the graph source:

| Stage | Production (live gate) | This batch (offline) |
|---|---|---|
| `CALLS` graph | GitLab Orbit | stdlib `ast`, scope/import-aware (conservative) |
| diff → changed symbols | Orbit line ranges | same line-range overlap, from the bug patch |
| coverage | test-name reference scan | same name-reference heuristic |
| **closure + gate + min test set + Shapley** | **`faultline-engine`** | **the same `faultline-engine` binary** |

Because the static graph under-approximates dynamic dispatch, the reported counts are a
**lower bound**; Orbit resolves more edges in production. Nothing is hard-coded — pass a
different `--project` (e.g. `tornado`, `luigi`, `thefuck`) to extend the batch.
