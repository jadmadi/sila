---
name: establish-visual-hierarchy
description: Determine visual focal points and prioritize attention order using size, font weight, color contrast, and deliberate de-emphasis instead of solely increasing heading font sizes. Use when designing layouts, reviewing screens with competing focal points, structuring cards or dashboards, or fixing noisy user interfaces.
domain: ui-design
---

# Establish Visual Hierarchy

## Overview

Visual hierarchy determines the order in which a user notices, reads, and processes elements on a screen. The most common UI mistake is treating visual weight as a single dial (font size) that gets turned up until headers are monstrous. Effective hierarchy is created by **de-emphasizing secondary and tertiary elements**, letting primary information command attention naturally.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## Trigger Scenarios

Activate this skill when:
- Reviewing or designing a screen where all elements compete for equal visual attention.
- Headings keep getting larger because they don't feel prominent enough.
- Complex dashboards, cards, or data tables feel overwhelming or noisy.
- Designing hero sections, detail views, or marketing landing pages.

---

## Visual Weight Decision Matrix

Visual weight is determined by four primary levers:

| Lever | High Priority (Primary) | Medium Priority (Secondary) | Low Priority (Tertiary / Ancillary) |
| :--- | :--- | :--- | :--- |
| **Size** | `text-xl` to `text-2xl` (Headers) | `text-base` (Body text) | `text-sm` to `text-xs` (Metadata, timestamps) |
| **Weight** | `font-bold` (700) or `font-semibold` (600) | `font-medium` (500) | `font-normal` (400) |
| **Color Contrast** | `text-slate-900` (High contrast) | `text-slate-600` (Medium contrast) | `text-slate-400` / `text-slate-500` (De-emphasized) |
| **Visual Element** | Solid high-contrast background | Subtle tinted background | Text only, no border/background |

---

## Core Refactoring Heuristics

### 1. De-Emphasize Instead of Over-Emphasizing
- **Anti-Pattern**: When title A doesn't stand out next to subtitle B, making title A 32px bold.
- **Remedy**: Keep title A at 18px semibold, and change subtitle B from 16px dark gray to 14px muted gray (`text-slate-500`). The contrast gap widens without inflating heading dimensions.

### 2. Labels Are Not Always Necessary
- **Anti-Pattern**: Labeling every piece of data explicitly: `Email: john@example.com`, `Role: Admin`, `Status: Active`.
- **Remedy**: Format the data so its role is self-evident. Make the name bold, email muted below it, and status a colored badge. Remove the literal words `Email:` and `Role:`.

### 3. Establish a Single Primary Visual Anchor per Scope
- In every bounded container (card, modal, hero view), there must be **exactly one** primary anchor that catches the eye first (e.g. key metric, primary title, or primary action).
- If three items are competing for visual dominance, demote two of them.

---

## Concrete Code Transformation

### Before (Competing Visual Weight)
```html
<div class="card p-4">
  <h2 class="text-xl font-bold text-black">Invoice #1042</h2>
  <span class="text-lg font-bold text-black">Total: $450.00</span>
  <span class="text-base font-bold text-black">Status: Paid</span>
  <span class="text-base font-bold text-black">Date: Sep 29, 2026</span>
</div>
```

### After (Calibrated Visual Hierarchy)
```html
<div class="rounded-xl bg-white p-6 shadow-sm border border-slate-100">
  <div class="flex items-center justify-between mb-3">
    <span class="text-xs font-semibold uppercase tracking-wider text-slate-400">Invoice #1042</span>
    <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-emerald-50 text-emerald-700 border border-emerald-100">Paid</span>
  </div>
  <div class="text-3xl font-extrabold text-slate-900 tracking-tight mb-1">$450.00</div>
  <p class="text-sm text-slate-500">Issued on September 29, 2026</p>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing an audit or layout refactor, verify:
- [ ] Exactly one primary visual focal point exists per container or card.
- [ ] Secondary metadata is de-emphasized using lighter color (`text-slate-500`) rather than tiny illegible font size.
- [ ] No decorative headings exceed reasonable proportions simply to outcompete adjacent text.
- [ ] Redundant labels (`Title:`, `Date:`, `Total:`) are removed where context makes meaning obvious.

### Self-Improvement Feedback Loop
If this skill encounters a dense UI layout where hierarchy cannot be resolved with standard de-emphasis, record a lesson in Sila:
```bash
sila lesson "Visual hierarchy failure on dense data grid" --cause="too many competing metrics" --fix="split into primary summary hero and secondary expandable drawer"
```
