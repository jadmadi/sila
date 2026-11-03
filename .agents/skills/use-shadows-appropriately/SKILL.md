---
name: use-shadows-appropriately
description: Apply multi-tiered elevation systems combining soft ambient blur and directional contact shadows while avoiding harsh pure-black drop shadows. Use when styling cards, dropdowns, floating modals, buttons, or elevated UI components.
domain: ui-design
---

# Use Shadows Appropriately

## Overview

Shadows in user interfaces simulate real-world light sources to establish depth, elevation, and tactile separation. A common UI mistake is applying harsh, high-opacity, single-layer pure black drop shadows (`box-shadow: 0 4px 6px rgba(0,0,0,0.4)`), which makes interfaces look dirty and dated. High-end modern interfaces construct shadows by layering **ambient light** (soft, diffuse, non-directional) with **direct light** (vertical offset, crisp contact shadow) and tinting shadows toward the background or brand color.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## Trigger Scenarios

Activate this skill when:
- Designing cards, modals, dropdowns, tooltips, or popovers.
- Elements feel flat or blend uncomfortably into adjacent containers.
- Existing shadows look muddy, harsh, or overly dramatic.
- Implementing dark mode elevation where standard shadows disappear against dark backgrounds.
- Creating interactive hover or active depth transitions.

---

## Elevation & Shadow Decision Matrix

| Elevation Tier | Use Case | Y-Offset / Blur | Shadow Layering Strategy (Tailwind / CSS) |
| :--- | :--- | :--- | :--- |
| **Tier 1: Subtle Resting** | Cards, input fields, flat buttons | `0 1px 2px` | `shadow-sm`: `0 1px 2px 0 rgba(0, 0, 0, 0.05)` |
| **Tier 2: Interactive / Floating** | Hovered cards, dropdowns, flyouts, popovers | `0 4px 6px` & `0 10px 15px` | `shadow-md` / `shadow-lg`: dual-layer ambient + directional |
| **Tier 3: Overlay / Dialog** | Modals, command palettes, sticky sheets | `0 20px 25px` & `0 25px 50px` | `shadow-xl` / `shadow-2xl`: deep vertical offset with diffused spread |

---

## Core Refactoring Heuristics

### 1. Two-Layer Shadow Technique
Natural shadows come from two light sources: ambient light (bouncing off walls/desks) and a primary direct overhead light.
- **Layer 1 (Direct Light)**: Small blur, noticeable vertical offset (e.g. `0 4px 6px -1px rgba(15, 23, 42, 0.1)`).
- **Layer 2 (Ambient Light)**: Larger blur, minimal offset (e.g. `0 2px 4px -2px rgba(15, 23, 42, 0.06)`).
- Combining both produces a realistic, soft yet grounded appearance without dark halos.

### 2. Never Use Pure Black on Colored or Light Backgrounds
- Pure black (`rgba(0, 0, 0, ...)`) on colored or tinted backgrounds turns gray and muddy.
- Tint the shadow with a tiny amount of the background hue or neutral slate/cool gray (e.g., `rgba(15, 23, 42, 0.08)` for slate themes, or `rgba(30, 41, 59, 0.1)`).
- For vibrant buttons (e.g. indigo CTA), a colored shadow matching the brand hue creates a glowing, premium look:
  `box-shadow: 0 10px 15px -3px rgba(79, 70, 229, 0.3)`.

### 3. Elevate with Lighter Surfaces in Dark Mode
- Shadows are virtually invisible against `#0f172a` or `#18181b`.
- In dark themes, depth is conveyed by **lightening the surface color** as it gets closer to the user:
  - Base background: `bg-slate-950`
  - Resting cards (Tier 1): `bg-slate-900`
  - Floating menus (Tier 2): `bg-slate-800`
  - Modals (Tier 3): `bg-slate-700`
- Pair with a subtle 1px border highlight (`border-white/10`) to define edges.

### 4. Shadow Size Signals Elevation, Not Importance
- Do not add larger shadows to a card simply to draw attention to it; larger shadows signify that an element is closer to the user in 3D space (modals, dropdowns).
- Use color or typography to signal importance on the same plane.

---

## Concrete Code Transformation

### Before (Muddy, Single-Layer Harsh Shadow)
```html
<div style="background: white; border-radius: 8px; box-shadow: 0 8px 16px rgba(0, 0, 0, 0.35); padding: 24px;">
  <h3 style="font-weight: bold; font-size: 18px; margin-bottom: 8px;">Delete Workspace</h3>
  <p style="color: #666; font-size: 14px; margin-bottom: 16px;">This action cannot be undone.</p>
  <button style="background: #e11d48; color: white; padding: 8px 16px; border-radius: 6px; box-shadow: 0 4px 8px black;">
    Confirm
  </button>
</div>
```

### After (Calibrated Two-Layer Ambient Shadow & Brand Glow)
```html
<!-- Modal surface with calibrated multi-layer shadow -->
<div class="rounded-2xl bg-white p-6 shadow-xl shadow-slate-900/10 ring-1 ring-slate-900/5">
  <div class="flex items-center gap-3 mb-2">
    <div class="flex h-10 w-10 items-center justify-center rounded-full bg-rose-50 text-rose-600">
      <svg class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
        <path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
      </svg>
    </div>
    <h3 class="text-base font-semibold text-slate-900">Delete Workspace</h3>
  </div>
  <p class="text-sm text-slate-500 mb-6">
    Are you sure you want to delete this workspace? All projects and telemetry will be permanently removed.
  </p>
  <div class="flex justify-end gap-3">
    <button class="px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 rounded-lg transition-colors">
      Cancel
    </button>
    <button class="px-4 py-2 text-sm font-semibold text-white bg-rose-600 hover:bg-rose-500 rounded-lg shadow-sm shadow-rose-600/30 transition-all hover:shadow-md hover:shadow-rose-600/40">
      Confirm Deletion
    </button>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing a shadow or depth refactor, verify:
- [ ] No single-layer high-opacity (`>0.2`) black shadows are used.
- [ ] Elevation tiers are consistent: resting cards Tier 1, menus Tier 2, dialogs Tier 3.
- [ ] In dark mode, depth is expressed primarily through background lightness tiers rather than invisible dark shadows.
- [ ] Colored button shadows match the button's hue rather than muddy black.
- [ ] Subtle borders (`ring-1 ring-slate-900/5` or `border border-slate-100`) reinforce shadow boundaries on low-contrast displays.

### Self-Improvement Feedback Loop
If an edge case arises (e.g. mobile Safari shadow clipping, SVG filter blur performance drops), log to Sila:
```bash
sila lesson "Shadow rendering artifact on overflow container" --cause="overflow-hidden clips child box-shadow" --fix="move shadow to parent wrapper or use ring/border fallback"
```
