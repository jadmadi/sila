---
name: build-color-palette
description: Disciplined 9-shade color ramps (50-900) for primary brands, warm/cool grays, semantic status colors (success, warning, error), and purposeful accent highlights. Use when establishing design tokens, picking UI colors, replacing arbitrary hex values, or fixing harsh contrast and pure-black interfaces.
domain: ui-design
---

# Build Color Palette

## Overview

Most applications suffer from either **too few colors** (causing developers to reach for one vibrant primary blue everywhere) or **uncontrolled color explosion** (dozens of discordant hex codes picked from a color picker). A production-grade design system requires pre-calculated **9-shade color ramps (50–900)** with tinted grays that match the brand's optical temperature.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## 9-Shade Ramp Structure

Every functional hue requires a 9-shade ramp:

| Shade Tier | Weight Range | Primary Purpose in Interface |
| :--- | :--- | :--- |
| **Lightest** | `50` | Page background tint, badge backgrounds, table hover states |
| **Light** | `100` – `200` | Subtle borders, divider lines, secondary button backgrounds |
| **Mid-Light** | `300` – `400` | Disabled input borders, placeholder text, inactive tabs |
| **Base** | `500` – `600` | Primary interactive elements (solid buttons, active toggles, key links) |
| **Dark** | `700` | Hover states for primary buttons, high-contrast text on light tints |
| **Darkest** | `800` – `900` | Primary headings, body copy, active states |

---

## Core Refactoring Heuristics

### 1. Never Use Pure Black (`#000000`) on Pure White (`#ffffff`)
- Pure `#000000` creates harsh optical vibration and excessive eye strain against pure white.
- Replace `#000000` with dark slate or zinc: `text-slate-900` (`#0f172a`) or `text-zinc-900` (`#18181b`).

### 2. Tint Your Grays with Brand Temperature
- True neutral gray (`#808080`) looks dull, lifeless, and clinical.
- **Cool Brand (Tech, SaaS, Data)**: Use slate or cool gray (tinted with a trace of blue/cyan: `#0f172a`, `#334155`, `#64748b`, `#f8fafc`).
- **Warm Brand (Editorial, Lifestyle, Food)**: Use warm stone or sand (tinted with amber/yellow: `#1c1917`, `#44403c`, `#78716c`, `#fafaf9`).

### 3. Semantic Colors Must Be Complete Systems
Never use semantic colors as single raw hex codes. Each semantic state needs its own light background, border, and dark text:

| Semantic State | 50 (Background) | 200 (Border) | 600 (Icon/Action) | 800 (Text) |
| :--- | :--- | :--- | :--- | :--- |
| **Success** | `bg-emerald-50` | `border-emerald-200` | `text-emerald-600` | `text-emerald-800` |
| **Warning** | `bg-amber-50` | `border-amber-200` | `text-amber-600` | `text-amber-800` |
| **Error / Danger** | `bg-rose-50` | `border-rose-200` | `text-rose-600` | `text-rose-800` |
| **Info** | `bg-sky-50` | `border-sky-200` | `text-sky-600` | `text-sky-800` |

---

## Concrete Code Transformation

### Before (Raw Hex Codes, Harsh Pure Black, Flat Colors)
```html
<div style="background-color: #ffffff; border: 1px solid #000000; padding: 16px;">
  <p style="color: #000000; font-weight: bold;">System Alert</p>
  <p style="color: #666666;">Database backup completed successfully.</p>
  <button style="background-color: #0000ff; color: #ffffff;">Dismiss</button>
</div>
```

### After (Tinted Slate Ramps and Semantic Tones)
```html
<div class="rounded-xl border border-emerald-200 bg-emerald-50/60 p-4 shadow-sm">
  <div class="flex items-start gap-3">
    <div class="rounded-full bg-emerald-100 p-1 text-emerald-600">
      <svg class="h-4 w-4" fill="currentColor" viewBox="0 0 20 20"><!-- Check icon --></svg>
    </div>
    <div class="flex-1">
      <h3 class="text-sm font-semibold text-emerald-900">Backup Succeeded</h3>
      <p class="mt-1 text-xs text-emerald-700">Database snapshot committed with zero WAL residue.</p>
    </div>
    <button class="text-xs font-medium text-emerald-800 hover:text-emerald-950 underline underline-offset-2">Dismiss</button>
  </div>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing a color palette review, verify:
- [ ] No pure black `#000000` text on pure white backgrounds.
- [ ] Gray scale is intentionally tinted (slate, zinc, or stone) matching brand temperature.
- [ ] Semantic banners (alerts, badges) use multi-stop color combinations (50 bg, 200 border, 700/800 text) rather than stark white text on vivid backgrounds.
- [ ] Interactive primary colors are reserved for actionable items, not decorative page elements.

### Self-Improvement Feedback Loop
If you detect an inaccessible color combination in dark mode or high-contrast theme, record a lesson in Sila:
```bash
sila lesson "Color contrast failure in dark mode badge" --cause="emerald-800 text unreadable on dark background" --fix="swap to emerald-400 text with emerald-950 background in dark mode"
```
