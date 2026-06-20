# Closing the loop with GitLab Duo

Faultline's gate doesn't just block — it names the **exact** test to add: the
*minimum test set* (a provably-minimal vertex cut). That makes the fix
automatable. So the verdict ends with a hand-off that mentions a GitLab Duo
**flow**, which can open a **draft** MR adding that test. A human still reviews
and approves; the gate never auto-merges.

```
gate fails (untested blast radius)
  → verdict names the minimum test set
  → @-mention a Duo flow with that goal        ← GitLab's documented flow trigger
  → Duo opens a DRAFT merge request adding the test
  → pipeline re-runs → blast radius now tested → gate passes
```

## What Faultline ships (in this repo, tested)

The gate emits the hand-off automatically. Set the CI/CD variable
`FAULTLINE_DUO_FLOW` to your flow's service-account handle (e.g. `@faultline-fix`)
and the verdict posts a ready-to-trigger mention with the precise goal; leave it
unset and the same line renders as guidance (never a fake @-ping). See
`agent/duo.go` and `agent/duo_test.go`.

## What you install (user-side, one-time)

The flow runs in **your** GitLab instance (GitLab Duo Agent Platform, currently
Beta) — it cannot be bundled in this repo. Two pieces:

1. A flow file at `.gitlab/duo/flows/<name>.yaml` (flow-registry **v1** schema).
2. A trigger: in the project, bind the flow to the event / service account you
   mention (configured in the UI under the AI / Duo **Triggers** settings, not in
   YAML).

### ⚠️ Honest note on the schema

The flow-registry v1 spec is **Beta and actively changing** (e.g. a recent GitLab
change made `unit_primitives` a required field). The published docs show the
`components` block verbatim but **not** a complete end-to-end example — they point
to the external v1 specification. **Treat the skeleton below as a starting point
and validate it against your instance's current
[custom flow YAML schema](https://docs.gitlab.com/user/duo_agent_platform/flows/custom_flows_schema/)
before relying on it.** We deliberately do not ship a flow we cannot execute here
and claim it "works".

### Verified building block (verbatim from GitLab docs)

A component receives the trigger goal through `inputs`:

```yaml
components:
  - name: "write_test"
    type: AgentComponent
    prompt_id: "write_test_prompt"
    inputs:
      - from: "context:project_id"
        as: "project_id"
      - "context:goal"        # = Faultline's hand-off text (the minimum test set)
```

Documented constraints for custom flows: `environment` must be `ambient`; the
`model` field inside a prompt is unsupported; the `name` / `description` /
`product_group` top-level fields are rejected.

### Skeleton flow (adapt + validate — NOT a tested artifact)

```yaml
version: v1
environment: ambient
components:
  - name: "write_test"
    type: AgentComponent
    prompt_id: "write_test_prompt"
    inputs:
      - from: "context:project_id"
        as: "project_id"
      - "context:goal"
prompts:
  - id: "write_test_prompt"
    prompt_template: |
      You are closing an untested blast radius flagged by Faultline.
      Goal: {{goal}}
      Add a focused unit test for the named symbol(s), run it, and open a DRAFT
      merge request. Do not modify unrelated code.
routers:
  - from: "write_test"
    to: "end"
flow:
  entry_point: "write_test"
```

`.gitlab/duo/agent-config.yml` controls CI execution (e.g. the `setup_script` that
installs dependencies before the flow runs).

## Why a draft MR (the honest boundary)

The loop ends at a **draft** MR on purpose. The deterministic gate decides *what*
must be tested; the LLM agent only *drafts* it; a human approves. Faultline never
lets a model auto-merge — the control plane stays deterministic and human-gated.
This is the same posture as the rest of Faultline: a model may assist, but it is
never in the compute path of the verdict.
