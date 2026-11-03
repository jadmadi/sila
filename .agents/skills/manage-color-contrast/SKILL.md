---
name: manage-color-contrast
description: Ensure accessible WCAG 2.2 AA / APCA contrast ratios, tint grays to match colored backgrounds, and eliminate harsh black-on-white vibration. Use when auditing text readability, styling colored hero sections, creating badge tags, or refining accessible themes.
domain: ui-design
---

# Manage Color Contrast

## Overview

Color contrast is the cornerstone of visual readability and accessibility. Uncalibrated interfaces frequently make two opposing mistakes: either producing unreadable low-contrast light gray text (`#94a3b8` on white body copy) failing WCAG thresholds, or using harsh pure black (`#000000`) on stark white (`#ffffff`), causing eye strain and chromatic vibration. Furthermore, placing neutral untinted grays on top of saturated colored backgrounds produces muddy, sickly text. Mastering color contrast requires calibrating luminance ratios and tinting neutrals to harmonize with their background canvas.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## Trigger Scenarios

Activate this skill when:
- Auditing UI accessibility for WCAG 2.2 AA or APCA compliance.
- Body text or placeholder text feels washed out or hard to read on low-brightness monitors.
- Text on saturated colored backgrounds (heroes, callout banners, buttons) looks dull or discordant.
- Designing status badges, notification toasts, or data tags.
- High contrast mode or dark theme legibility needs refinement.

---

## Contrast Standards & Ratio Reference

| Element Type | WCAG 2.2 AA Min Ratio | APCA Target | Recommended Tailwind / Hex Values |
| :--- | :--- | :--- | :--- |
| **Normal Body Text** (<18pt / 24px) | **4.5:1** | Lc 60–75 | `text-slate-700` (`#334155`) to `text-slate-900` (`#0f172a`) on `#ffffff` |
| **Large Text / Headings** (≥18pt or 14pt bold) | **3.0:1** | Lc 45–60 | `text-slate-800` (`#1e293b`) on `#ffffff` |
| **UI Components & Borders** | **3.0:1** | Lc 45 | Form boundaries: `border-slate-300` (`#cbd5e1`) or `border-slate-400` |
| **De-Emphasized Secondary Text** | **4.5:1** | Lc 60 | `text-slate-500` (`#64748b`) or `text-slate-600` (`#475569`) |
| **Disabled Controls & Decorative** | No minimum | — | `text-slate-300` / `text-slate-400` with explicit `aria-disabled="true"` |

---

## Core Refactoring Heuristics

### 1. Eliminate Pure Black Text on Pure White
- **Anti-Pattern**: Setting `color: #000000; background: #ffffff;`. The 21:1 extreme contrast ratio causes visual vibration, halos, and rapid ocular fatigue.
- **Remedy**: Use a deep dark slate or charcoal neutral (`#0f172a` / `text-slate-900` or `#111827` / `text-gray-900`). The contrast drops to a comfortable ~16:1, maintaining crisp readability with a softer, premium reading experience.

### 2. Tint Grays to the Background Surface
- Neutral grays placed on saturated backgrounds (e.g. `#64748b` on a navy `#1e3a8a` hero) look artificially washed out and chemically mismatched because the eye compares gray to the surrounding saturation.
- **Remedy**:
  - On a dark blue background, tint text toward light cyan/blue: `text-blue-100` or `text-blue-200`.
  - On a dark purple background, tint text toward soft lavender: `text-purple-200`.
  - On a warm cream/stone background, use warm stone grays (`text-stone-700`) instead of cool zinc.

### 3. High-Contrast Status Badges (Light Surface + Dark Text)
- **Anti-Pattern**: White text on light pastel backgrounds (e.g. `#ffffff` text on light green `#86efac` badge) fails contrast catastrophically (~1.8:1).
- **Remedy**: Pair a soft tinted background with a deep, saturated text color from the same family:
  - Success: `bg-emerald-50 text-emerald-800 border-emerald-200` (Contrast > 7:1).
  - Warning: `bg-amber-50 text-amber-900 border-amber-200` (Contrast > 8:1).
  - Danger: `bg-rose-50 text-rose-800 border-rose-200` (Contrast > 7:1).

### 4. Never Rely Solely on Color for Information
- Color-blind users cannot differentiate green success from red danger. Always accompany color with an icon, semantic prefix, or explicit status label (`aria-label` or visible text).

---

## Concrete Code Transformation

### Before (Failing Contrast & Clashing Untinted Grays)
```html
<!-- Dark navy hero with dead gray text and unreadable pill badge -->
<section style="background-color: #1e3a8a; padding: 48px; color: white;">
  <!-- Gray on blue looks muddy; badge has unreadable white on lime green -->
  <span style="background-color: #84cc16; color: white; padding: 4px 8px; border-radius: 4px; font-size: 12px;">
    Active
  </span>
  <h1 style="font-size: 32px; font-weight: bold; margin-top: 16px;">Telemetry Pipeline</h1>
  <p style="color: #94a3b8; font-size: 16px; margin-top: 8px;">
    Real-time event processing and edge draining across global clusters.
  </p>
</section>
```

### After (Calibrated Tinted Luminance & Accessible Contrast)
```html
<section class="bg-indigo-950 px-8 py-12 text-white rounded-2xl">
  <!-- Status pill using dark saturated text on luminous background -->
  <div class="flex items-center gap-2">
    <span class="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-emerald-400/10 text-emerald-300 border border-emerald-400/20">
      <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
      Active Cluster
    </span>
  </div>
  <h1 class="text-3xl font-extrabold text-white tracking-tight mt-4">Telemetry Pipeline</h1>
  <!-- Text tinted with indigo-200 harmonizes with dark indigo canvas at 8.2:1 contrast -->
  <p class="text-indigo-200 text-base max-w-2xl mt-2 leading-relaxed">
    Real-time event processing and edge draining across global clusters with guaranteed zero data loss.
  </p>
</section>
```

---

## Verification & Self-Improvement Rubric

Before completing a color contrast review, verify:
- [ ] Body copy achieves at least 4.5:1 contrast against its background container.
- [ ] No pure black (`#000000`) text sits on pure white (`#ffffff`) canvases.
- [ ] Colored backgrounds feature tinted neutral text rather than desaturated cold gray.
- [ ] Status pills pair light backgrounds with deep dark text (or dark backgrounds with light text) meeting ≥ 4.5:1.
- [ ] Form input borders are visible and achieve ≥ 3.0:1 contrast against adjacent page surfaces.

### Self-Improvement Feedback Loop
If an audited design system uses brand colors that cannot achieve 4.5:1 without darkening into black, log to Sila:
```bash
sila lesson "Brand accent color failed accessible contrast" --cause="brand yellow #eab308 cannot support white text" --fix="pair brand yellow with black text, or reserve brand yellow for borders/accents"
```
