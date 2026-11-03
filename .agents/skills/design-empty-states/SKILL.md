---
name: design-empty-states
description: Transforming zero-data states into high-value onboarding experiences with contextual illustrations, empathetic copy, and prominent primary setup actions. Use when designing first-time user flows, zero-search results screens, empty lists, dashboards with no activity, or missing item views.
domain: ui-design
---

# Design Empty States

## Overview

An empty state is often the very first thing a user sees after signing up or creating an account. In most applications, it is treated as an afterthought: a blank white box with a cold, passive string like `"No items found."` This kills user momentum and causes immediate bounce. A well-crafted empty state acts as an **onboarding concierge** that explains the value of the feature and provides an immediate, frictionless call-to-action to populate it.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## The 4-Part Anatomy of an Effective Empty State

Every production empty state must contain four deliberate layers:

```
┌────────────────────────────────────────────────────────┐
│                                                        │
│                    [ 🎨 Visual Icon ]                  │
│                                                        │
│                  1. Clear, Inspiring Headline          │
│                  2. Empathetic Explanatory Copy        │
│                                                        │
│               [ 3. Prominent Primary CTA Button ]      │
│                                                        │
│               4. Optional Secondary Link / Docs        │
│                                                        │
└────────────────────────────────────────────────────────┘
```

1. **Visual Anchor**: A muted, friendly icon or spot illustration enclosed in a soft tinted circle (`w-12 h-12 rounded-full bg-slate-100`). Avoid alarming warning symbols.
2. **Action-Oriented Headline**: State what is possible, not what is missing (`"Create your first knowledge base"` instead of `"0 items found"`).
3. **Orientation Copy**: 1–2 sentences explaining why this feature exists and what happens when populated. Keep line length tight (`max-w-sm`).
4. **Frictionless Primary CTA**: A high-contrast primary button that directly opens the creation modal or triggers the setup workflow.

---

## Core Refactoring Heuristics

### 1. Distinguish First-Run Empty States from Zero-Search Empty States
- **First-Run State**: The user has never created anything. Focus on onboarding, feature benefits, and the primary create action.
- **Zero-Search State**: The user searched for a query that yielded no results. Focus on diagnosing the filter: `"No files matching 'payment'"`, with a one-click button to `"Clear search filters"`.

### 2. Never Use Broken or Disabled Table Shells
- **Anti-Pattern**: Rendering an empty `<table>` with headers (`Name`, `Date`, `Size`) and a single row spanning 5 columns saying `"No data"`.
- **Remedy**: Replace the entire table container with a dedicated empty state card or canvas.

### 3. Provide Starter Templates or Sample Data
- Where applicable, offer a secondary action to "Load sample data" or "Choose from template". This allows new users to evaluate the UI without doing heavy initial setup.

---

## Concrete Code Transformation

### Before (Cold, Passive, Unhelpful)
```html
<div class="p-8 text-center border">
  <p class="text-gray-500">No projects found.</p>
</div>
```

### After (High-Value Onboarding Concierge)
```html
<div class="rounded-2xl border-2 border-dashed border-slate-200 bg-white p-12 text-center max-w-xl mx-auto my-8">
  <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-indigo-50 text-indigo-600 shadow-sm mb-4">
    <svg class="h-7 w-7" fill="none" stroke="currentColor" stroke-width="1.75" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
    </svg>
  </div>
  <h3 class="text-base font-semibold text-slate-900 tracking-tight">No projects created yet</h3>
  <p class="mt-2 text-sm text-slate-500 max-w-sm mx-auto leading-relaxed">
    Projects organize your codebase facts, architectural memos, and autonomous agent goals in one local store.
  </p>
  <div class="mt-6 flex flex-col sm:flex-row items-center justify-center gap-3">
    <button class="w-full sm:w-auto inline-flex items-center justify-center gap-2 rounded-xl bg-indigo-600 px-4 py-2.5 text-xs font-semibold text-white shadow-sm hover:bg-indigo-700 transition-all focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2">
      <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M12 4.5v15m7.5-7.5h-15"/></svg>
      Create New Project
    </button>
    <button class="w-full sm:w-auto text-xs font-semibold text-slate-600 hover:text-slate-900 px-3 py-2">
      View Documentation →
    </button>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing an empty state design, verify:
- [ ] No raw "No data found" passive labels.
- [ ] Visual anchor (icon or illustration) has appropriate tone (welcoming, not an error).
- [ ] Primary button directly launches the creation flow.
- [ ] Search zero-results states provide a quick "Clear filters" action.

### Self-Improvement Feedback Loop
If an empty state CTA fails because required onboarding prerequisites (e.g. API keys) are missing, record a lesson in Sila:
```bash
sila lesson "Empty state CTA blocked by missing credentials" --cause="button launched create flow before config was initialized" --fix="check config state and guide user through one-step setup modal first"
```
