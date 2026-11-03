---
name: group-related-elements
description: Apply Gestalt proximity principles, card enclosure padding ratios, and explicit alignment anchors to group related interface elements. Use when structuring forms, complex cards, dashboards, navigation lists, or fixing fragmented layouts.
domain: ui-design
---

# Group Related Elements

## Overview

Human perception naturally organizes visual fields into units based on Gestalt psychology. The most powerful grouping mechanism is **proximity**: elements positioned near each other are perceived as a single entity, while elements separated by larger gaps are perceived as distinct. A frequent layout defect is applying uniform spacing across all elements (e.g. 16px between label and input, and 16px between input and the next label), which causes inputs to float ambiguously between labels. Grouping is reinforced through **enclosure** (cards, subtle panels) and consistent **alignment anchors**.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## Trigger Scenarios

Activate this skill when:
- Form fields look ambiguous, making it hard to discern which label belongs to which input.
- Cards, dashboards, or detail lists feel visually disorganized or fragmented.
- Sibling elements feel detached from their parent container.
- Container padding is smaller than the spacing between children inside it (enclosure collision).
- Icons, text labels, and badges sit slightly out of alignment along shared axes.

---

## Gestalt Proximity & Spacing Hierarchy

| Spatial Relationship | Recommended Spacing (Tailwind) | Rationale |
| :--- | :--- | :--- |
| **Intra-Element** (Icon to text) | `gap-1.5` to `gap-2` (6–8px) | Tight bond; read as a single atomic unit |
| **Direct Parent-Child** (Label to input) | `mb-1.5` to `mb-2` (6–8px) | Strong connection; label unambiguously claims input |
| **Sibling Fields** (Form group to form group) | `space-y-5` to `space-y-6` (20–24px) | Must be **at least 2.5× to 3× larger** than label-to-input gap |
| **Container Padding** (Card border to content) | `p-6` to `p-8` (24–32px) | Must be **larger than internal element gaps** |
| **Major Page Sections** | `gap-12` to `gap-16` (48–64px) | Distinct thematic boundaries |

---

## Core Refactoring Heuristics

### 1. Label-Input Proximity Rule (The 3× Ratio)
- If the gap between a label and its input is 8px, the gap to the *next* form group must be at least 24px (3×).
- If both gaps are equal (e.g. 16px label gap and 16px group gap), users instinctively associate the input with the label *below* it.

### 2. The Enclosure Padding Rule
- When enclosing elements inside a card or container, the container padding must exceed the spacing between the enclosed items:
  `Container Padding > Intra-Child Spacing`.
- Example: If a card contains list items separated by 16px gaps, the card padding must be at least 20px–24px (`p-5` or `p-6`). If the card padding is only 12px, the enclosed content feels suffocated and presses uncomfortably against the container perimeter.

### 3. Anchor by Alignment
- Align elements along a strong, shared linear axis.
- Mixed left-alignment and center-alignment within the same card creates an unsettled, amateur look. Keep headings, body text, and primary actions strictly left-aligned unless styling a dedicated hero or centered modal.
- Center icons vertically with the first line of text (`items-start pt-0.5` or `inline-flex items-center`) rather than centering across multi-line paragraphs.

### 4. Group with Background Tints Before Adding Borders
- Rather than drawing explicit boxes or borders around every group, place related secondary elements on a slightly different background shade (e.g. `bg-slate-50` or `bg-slate-900/40`).
- Background grouping feels natural and avoids the visual clutter of intersecting wireframe borders.

---

## Concrete Code Transformation

### Before (Ambiguous Proximity & Inverted Padding)
```html
<!-- Equal spacing makes labels ambiguous; card padding is smaller than field gap -->
<div style="background: white; border: 1px solid #ccc; padding: 12px;">
  <div style="margin-bottom: 20px;">
    <div style="margin-bottom: 20px; font-weight: bold;">Full Name</div>
    <input type="text" style="border: 1px solid #999; padding: 8px; width: 100%;" />
  </div>
  <div style="margin-bottom: 20px;">
    <div style="margin-bottom: 20px; font-weight: bold;">Work Email</div>
    <input type="email" style="border: 1px solid #999; padding: 8px; width: 100%;" />
  </div>
</div>
```

### After (Calibrated Proximity & Generous Enclosure)
```html
<div class="rounded-2xl bg-white p-8 shadow-sm border border-slate-100 max-w-md">
  <div class="space-y-6">
    <!-- Group 1: Tight label-to-input gap (6px) relative to section gap (24px) -->
    <div>
      <label for="name" class="block text-sm font-semibold text-slate-900 mb-1.5">
        Full Name
      </label>
      <input
        id="name"
        type="text"
        placeholder="Ada Lovelace"
        class="block w-full rounded-lg border border-slate-300 px-3.5 py-2 text-sm text-slate-900 placeholder:text-slate-400 focus:border-indigo-600 focus:ring-2 focus:ring-indigo-600/20 outline-none transition"
      />
    </div>

    <!-- Group 2 -->
    <div>
      <label for="email" class="block text-sm font-semibold text-slate-900 mb-1.5">
        Work Email
      </label>
      <input
        id="email"
        type="email"
        placeholder="ada@computing.org"
        class="block w-full rounded-lg border border-slate-300 px-3.5 py-2 text-sm text-slate-900 placeholder:text-slate-400 focus:border-indigo-600 focus:ring-2 focus:ring-indigo-600/20 outline-none transition"
      />
      <p class="mt-1.5 text-xs text-slate-500">We will send verification credentials here.</p>
    </div>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing an element grouping refactor, verify:
- [ ] Gap between distinct groups is at least 2.5× to 3× larger than the internal gap between a label and its control.
- [ ] Card/container padding (`padding`) strictly exceeds internal child item spacing (`gap` or `margin`).
- [ ] All elements within a container share consistent horizontal or vertical alignment anchors.
- [ ] Icons paired with multi-line text are anchored to the top baseline (`items-start pt-0.5`) rather than centered ambiguously in vertical space.
- [ ] Proximity and background enclosure are used to group items before resorting to heavy dividing lines.

### Self-Improvement Feedback Loop
If an edge case arises (e.g. nested sub-forms where spacing explodes vertically), log to Sila:
```bash
sila lesson "Nested form spacing explosion" --cause="strict 3x rule caused child subforms to exceed viewport" --fix="use compact visual card enclosure (bg-slate-50) with internal 12px gaps"
```
