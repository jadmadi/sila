# Distillation Engine (`--llm=stdio`)

`sila` incorporates an offline, deterministic LLM distillation layer that upgrades raw indexed session messages into atomic, permanent codebase facts.

---

## 1. Overview & Protocol Contract

The distillation pipeline reads pending raw claims from `sila.db`, executes a structured prompt contract over line-delimited JSON streams, and writes back atomic facts and updated distillation statuses.

### Distillation Request Schema (JSON per line)
```json
{
  "claim_id": "fact_a1b2c3d4",
  "project_id": "example-app",
  "raw_message": "Done. Built and deployed almatjar-store-manager so the /dashboard/tree toggle is live.",
  "repo_files": ["apps/store-manager/routes.ts", "scripts/deploy.sh"],
  "tools": ["devin", "mimocode"],
  "session_ref": "session_abc123"
}
```

### Distillation Response Schema (JSON per line)
```json
{
  "fact": "scripts/deploy.sh deploys almatjar-store-manager enabling the /dashboard/tree toggle.",
  "confidence": "high",
  "reasoning": "Technical deployment statement with verified repository scripts."
}
```

### Status Outcomes
- `distilled`: LLM returned a non-empty atomic sentence (`≤200` chars) grounded in `repo_files`.
- `skipped`: Message contains no permanent architectural/code facts (status announcements, greetings, chatter). `fact` is empty `""`.
- `failed`: Parse error, invalid JSON, or broken pipe. Can be re-queued with `--retry-failed`.

---

## 2. Running Distillation via `stdio`

`sila` remains completely offline, CGO-free, and SDK-independent by communicating over `stdio`. You can pipe the `stdio` distillation stream directly into an external summarization script, local Ollama runner, or OpenAI-compatible API.

### Example: Running with Python Bridge to OpenAI / Anthropic / Local LLM

```python
#!/usr/bin/env python3
import sys, json, urllib.request

def summarize(req):
    prompt = f"""You are a codebase knowledge distiller. Turn this assistant message into one concise factual sentence (≤200 chars).
Project: {req['project_id']}
Files: {', '.join(req['repo_files'])}
Tools: {', '.join(req['tools'])}
Message: {req['raw_message']}

Output ONLY JSON: {{"fact": "<one sentence>", "confidence": "high|mid|low", "reasoning": "<brief>"}}
If no permanent technical fact exists, return: {{"fact": "", "confidence": "low", "reasoning": "no factual claim"}}
"""
    # Call your preferred LLM provider here
    # (e.g. OpenAI, Anthropic, or local Ollama endpoint)
    # Output single JSON line to stdout:
    return {"fact": "Summary of change", "confidence": "high", "reasoning": "Grounded in code"}

for line in sys.stdin:
    if not line.strip():
        continue
    req = json.loads(line)
    result = summarize(req)
    print(json.dumps(result), flush=True)
```

Run distillation:
```bash
sila dream --llm=stdio < distill_bridge.py
```

---

## 3. Querying Distilled Facts

Once distilled, promoted facts (`corroborated` + `file_refs_exist` + `distilled`) can be searched exclusively:

```bash
# Search only true, verified distilled facts
sila dream query auth --facts-only

# Or query via MCP JSON-RPC
# tools/call: search_knowledge {"query": "auth", "facts_only": true}
```
