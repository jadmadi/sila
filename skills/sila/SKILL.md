---
name: "sila"
description: "Unified entrypoint across AI coding harnesses. Runs sila salam, identifies ready tasks, selects the appropriate playbook, and unrolls the 7-step stage gate."
---

# Unified Sila Sticky Mode

Execute the end-to-end Sila stage-gate protocol for any goal or roadmap task.
Typing /sila <goal> runs the full autonomous execution lifecycle:

1. Orient and Prime:
   Run sila salam and sila prime to load active repository context and invariants.

2. Goal Discovery and Chartering:
   Evaluate sila goals ready to select candidate tasks. Claim the goal atomically
   with sila goals claim <slug> --agent=<name> to establish the GoalCharter boundary.

3. Playbook Selection:
   Match the task shape to the correct specialized playbook:
   - runtime-forensics: For reproducing and diagnosing bugs under live execution.
   - visual-parity: For UI, layout, typography, or styling parity checks.
   - pr-babysitter: For triage, review response, and continuous gate maintenance.
   - characterization-refactor: For refactoring with safety harnesses.
   - autonomous-run: For long-running multi-task execution without human pauses.

4. Implementation:
   Write minimal, clean, production-grade Go code adhering to repository invariants.
   Verify zero CGO dependencies and run race tests with go test -race ./...

5. Stage Gate Validation:
   Execute sila gate check to confirm passing format, tests, and charter sentries.
   Run the rebase drift sentry to ensure zero semantic divergence from trunk.

6. Mystery Audit:
   Run sila goals audit <slug> or execute independent black-box probes.

7. Plain English Communication Hygiene:
   Run sila unslop to ensure communications remain free of sycophancy, buzzwords,
   excessive bold formatting, and em dashes.
