---
name: design-button-hierarchy
description: Clear tiering of interactive actions - single primary solid button per visual scope, secondary outline/soft buttons, and tertiary ghost/text links. Use when designing forms, dialog footers, cards with action buttons, or fixing screens with multiple competing primary calls-to-action.
domain: ui-design
---

# Design Button Hierarchy

## Overview

When every button on a screen is styled as a vibrant solid block of color, the user is left with no visual clue about what to do next. Visual fatigue sets in, decision paralysis occurs, and destructive mistakes happen. A polished interface provides a clear **three-tier action hierarchy** where every action signals its exact priority and consequence.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## The Three-Tier Action System

| Tier | Visual Treatment | Purpose & Usage | Tailwind Example |
| :--- | :--- | :--- | :--- |
| **Tier 1: Primary** | Solid vibrant background, high-contrast text | The single most important action on the page/modal. Limit to **one** per visual scope. | `bg-indigo-600 text-white hover:bg-indigo-700 shadow-sm` |
| **Tier 2: Secondary** | Subtle tinted background OR neutral outline with border | Important supporting actions (Save Draft, Filter, Export). | `bg-white border border-slate-300 text-slate-700 hover:bg-slate-50` |
| **Tier 3: Tertiary** | Ghost button (no border, no background until hover) | Low-emphasis options (Cancel, Skip, View Details). | `text-slate-600 hover:text-slate-900 hover:bg-slate-100/60` |
| **Destructive** | Soft red background or neutral until hover; red outline | Dangerous actions (Delete, Purge, Revoke). | `text-rose-600 hover:bg-rose-50 border border-rose-200` |

---

## Core Refactoring Heuristics

### 1. Exactly One Primary Action per Visual Viewport
- If a modal footer has "Save Changes" and "Cancel", only "Save Changes" is primary. "Cancel" should be a tertiary ghost button or secondary neutral outline.
- If a screen has "Export CSV", "Filter", and "Create New Project", only "Create New Project" deserves a solid primary background.

### 2. De-Emphasize Destructive Actions Until Engagement
- **Anti-Pattern**: A bright neon-red solid primary button for "Delete Account" sitting right next to "Save Profile", drawing 100% of the user's attention.
- **Remedy**: Style the destructive action as a secondary outline or tertiary text link (`text-rose-600 hover:bg-rose-50`). Only promote to a solid red button inside the final confirmation dialog.

### 3. Order Buttons Consistently by Platform Idiom
- For web applications and dashboards:
  - Place primary action on the right in multi-step flows: `[ Cancel ]  [ Next Step → ]`
  - Or align actions to the start/left in document/form editors: `[ Save Changes ]  [ Cancel ]`
  - Keep the spatial order consistent across the entire application.

---

## Concrete Code Transformation

### Before (Competing Primary Actions)
```html
<!-- Modal Footer with competing visual noise -->
<div class="flex justify-end gap-3 p-4 border-t">
  <button class="bg-red-600 text-white font-bold py-2 px-4 rounded">Delete Project</button>
  <button class="bg-gray-600 text-white font-bold py-2 px-4 rounded">Cancel</button>
  <button class="bg-blue-600 text-white font-bold py-2 px-4 rounded">Save Changes</button>
</div>
```

### After (Tiered Hierarchy and Muted Danger)
```html
<!-- Calibrated 3-tier action system -->
<div class="flex items-center justify-between p-4 border-t border-slate-100 bg-slate-50/50 rounded-b-xl">
  <!-- Destructive action isolated on the left, styled softly -->
  <button class="text-xs font-semibold text-rose-600 hover:text-rose-700 hover:bg-rose-50 px-3 py-1.5 rounded-lg transition-colors">
    Delete Project
  </button>

  <!-- Primary and secondary actions grouped on the right -->
  <div class="flex items-center gap-2">
    <button class="text-xs font-semibold text-slate-600 hover:text-slate-800 px-3.5 py-2 rounded-lg hover:bg-slate-200/50 transition-colors">
      Cancel
    </button>
    <button class="text-xs font-semibold text-white bg-indigo-600 hover:bg-indigo-700 px-4 py-2 rounded-lg shadow-sm transition-all focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2">
      Save Changes
    </button>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing a button or form review, verify:
- [ ] No more than one solid primary button exists in the immediate visual scope.
- [ ] Secondary actions are styled with neutral borders or soft tinted backgrounds.
- [ ] Dismissive or cancel actions are styled as tertiary ghost buttons without heavy borders.
- [ ] Destructive actions do not dominate page attention prior to user intent.

### Self-Improvement Feedback Loop
If you encounter a multi-action dropdown or button group where primary status is ambiguous, record a lesson in Sila:
```bash
sila lesson "Split button hierarchy collision" --cause="both dropdown trigger and main action used solid primary fill" --fix="make main action solid primary and dropdown chevron a subtle connected divider"
```
