---
name: apply-consistent-spacing
description: Strict 4pt/8pt spacing scale (4, 8, 12, 16, 24, 32, 48, 64px) eliminating arbitrary pixel margins and ensuring consistent rhythm between nested elements. Use when laying out components, fixing cramped or sparse forms, aligning grid items, or establishing baseline vertical cadence.
domain: ui-design
---

# Apply Consistent Spacing

## Overview

Inconsistent spacing is the number one reason an interface looks "amateur" or "cobbled together." When a developer chooses `margin-top: 13px`, `padding: 18px`, and `gap: 7px` arbitrarily, the eye detects visual friction without consciously understanding why. Clean UI design relies on a **strict 4pt/8pt linear modular spacing scale** and mathematical relationships between inner and outer spacing.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## The 4pt / 8pt Spacing Scale

Every margin, padding, gap, and dimension must originate from this fixed scale:

| Class | Pixel Value | Rem Value | Core Application |
| :--- | :--- | :--- | :--- |
| `p-1`, `m-1`, `gap-1` | 4px | 0.25rem | Micro-spacing (icon to text, tight tags) |
| `p-2`, `m-2`, `gap-2` | 8px | 0.50rem | Compact button padding, input internal padding |
| `p-3`, `m-3`, `gap-3` | 12px | 0.75rem | Standard button vertical padding, table cell vertical |
| `p-4`, `m-4`, `gap-4` | 16px | 1.00rem | Standard container padding, form field gap |
| `p-6`, `m-6`, `gap-6` | 24px | 1.50rem | Card internal padding, section internal margins |
| `p-8`, `m-8`, `gap-8` | 32px | 2.00rem | Modal dialog padding, large card padding |
| `p-12`, `m-12`, `gap-12` | 48px | 3.00rem | Distinct section separators, page hero top offset |
| `p-16`, `m-16`, `gap-16` | 64px | 4.00rem | Major page section divisions, footer top margin |

---

## Core Refactoring Heuristics

### 1. The Container Rule: Outer Spacing > Inner Spacing
- Spacing inside a child element must never exceed the padding of its parent container.
- If a card has `p-6` (24px) internal padding, spacing between items inside that card should be `gap-4` (16px) or `gap-3` (12px), never `gap-8` (32px).
- Elements grouped inside a container should feel like a cohesive unit, which requires container padding to anchor them securely.

### 2. Grouping Spacing: Tighter for Related, Looser for Distinct
- Distance between a form label and its input: `mb-1.5` (6px) or `mb-2` (8px).
- Distance between one form field group and the next form field group: `mb-5` (20px) or `mb-6` (24px).
- The gap between groups must be **at least 2.5x to 3x larger** than the gap inside the group so the Gestalt law of proximity functions immediately.

### 3. Asymmetric Button Padding
- Interactive buttons look boxy and stubby when horizontal and vertical padding are equal (`p-3`).
- Buttons must have **wider horizontal padding than vertical padding** (roughly 1.5x to 2x wider):
  - Standard button: `px-4 py-2` (16px horizontal, 8px vertical) or `px-5 py-2.5`.
  - Large button: `px-6 py-3`.

---

## Concrete Code Transformation

### Before (Arbitrary Pixel Values and Inverted Hierarchy)
```html
<div style="padding: 10px; margin: 5px;">
  <label style="margin-bottom: 25px; display: block;">Database Hostname</label>
  <input style="padding: 15px; margin-bottom: 8px;" type="text" />
  <label style="margin-bottom: 25px; display: block;">Port</label>
  <input style="padding: 15px;" type="text" />
</div>
```

### After (Strict 8pt Cadence and Proportional Grouping)
```html
<div class="rounded-xl bg-white p-6 shadow-sm border border-slate-200">
  <div class="space-y-4">
    <div>
      <label class="block text-xs font-semibold uppercase tracking-wider text-slate-700 mb-1.5">Database Hostname</label>
      <input class="w-full rounded-lg border border-slate-300 px-3.5 py-2 text-sm text-slate-900 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500" type="text" value="127.0.0.1" />
    </div>
    <div>
      <label class="block text-xs font-semibold uppercase tracking-wider text-slate-700 mb-1.5">Port</label>
      <input class="w-full rounded-lg border border-slate-300 px-3.5 py-2 text-sm text-slate-900 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500" type="text" value="5432" />
    </div>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing a layout review, verify:
- [ ] Every spacing utility strictly aligns with the 4pt/8pt scale (multiples of 4px).
- [ ] Parent container padding is strictly greater than internal element spacing.
- [ ] Form labels are positioned significantly closer to their own inputs than to preceding form fields.
- [ ] Interactive buttons feature asymmetric horizontal padding (horizontal $\ge 1.5 \times$ vertical).

### Self-Improvement Feedback Loop
If you encounter a legacy layout where converting to the 8pt scale breaks responsive breakpoints, record a lesson in Sila:
```bash
sila lesson "8pt spacing overflow on mobile viewport" --cause="p-8 padding on cards creates horizontal scroll on 320px screens" --fix="apply responsive padding p-4 sm:p-6 md:p-8"
```
