---
name: apply-typography-scale
description: Hand-crafted modular typographic scales (12, 14, 16, 18, 20, 24, 30, 36, 48px), proportional line-heights (tighter on large headers, looser on body text), and avoiding arbitrary pixel sizes or em inheritance compounding. Use when styling text, setting up design systems, establishing typography styles, or debugging cramped or disjointed text.
domain: ui-design
---

# Apply Typography Scale

## Overview

Typographic hierarchy breaks down when developers choose arbitrary font sizes (`13px`, `17px`, `21px`) on the fly, or rely on mathematical ratio scales (like the golden ratio 1.618) which produce numbers unusable for real-world interface text. Professional UIs rely on a **hand-crafted modular scale** where every level has a dedicated, repeatable purpose, paired with inversely proportional line heights.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## The Canonical Typography Scale

| Class | Font Size | Line Height | Letter Spacing | Typical Usage |
| :--- | :--- | :--- | :--- | :--- |
| `text-xs` | 12px (0.75rem) | 16px (1.33) | `tracking-wider` (+0.05em) | Uppercase badges, table headers, timestamps |
| `text-sm` | 14px (0.875rem) | 20px (1.43) | `tracking-normal` (0) | Dense UI body, form labels, helper text |
| `text-base` | 16px (1.000rem) | 24px (1.50) | `tracking-normal` (0) | Default interface text, article body copy |
| `text-lg` | 18px (1.125rem) | 28px (1.55) | `tracking-normal` (0) | Card headlines, prominent form inputs |
| `text-xl` | 20px (1.250rem) | 28px (1.40) | `tracking-tight` (-0.015em) | Section headers, modal titles |
| `text-2xl` | 24px (1.500rem) | 32px (1.33) | `tracking-tight` (-0.02em) | Page titles, primary dashboard metrics |
| `text-3xl` | 30px (1.875rem) | 36px (1.20) | `tracking-tight` (-0.025em) | Hero headlines, primary KPI stats |
| `text-4xl` | 36px (2.250rem) | 40px (1.11) | `tracking-tighter` (-0.03em) | Marketing splash titles, display text |

---

## Core Refactoring Heuristics

### 1. Inversely Proportional Line Height
- **Rule**: As font size increases, line height must decrease proportionally.
- Large display headings (`32px+`) look detached and disjointed with standard `1.5` line height. Give them tight line height (`1.1` to `1.25`).
- Small body text (`14px–16px`) needs generous breathing room (`1.5` to `1.65`) for optimal readability across multi-line paragraphs.

### 2. Tighten Letter Spacing on Large Headings
- Font glyphs are designed with spacing intended for body reading. At large sizes (`text-3xl+`), that built-in space creates noticeable gaps between letters.
- Use negative letter spacing (`tracking-tight` or `-0.02em`) on large, bold display titles.
- Use positive letter spacing (`tracking-wider` or `+0.05em`) on small, all-caps labels or tags (`text-xs uppercase`).

### 3. Enforce Line Length Budget (Measure)
- Paragraph text must never stretch across full-width monitors. Optimal reading measure is **45 to 75 characters per line** (approximately 600px to 750px).
- In Tailwind: wrap long text blocks in `max-w-prose` (65ch) or `max-w-2xl`.

---

## Concrete Code Transformation

### Before (Arbitrary Sizes and Loose Heading Spacing)
```html
<div>
  <h1 style="font-size: 34px; line-height: 1.6; letter-spacing: 0px;">
    Modern Developer Continuity
  </h1>
  <p style="font-size: 13px; line-height: 1.2;">
    Autonomous agent substrate running on physical hardware with zero telemetry.
  </p>
</div>
```

### After (Modular Scale with Tuned Line-Height and Tracking)
```html
<div class="max-w-prose">
  <h1 class="text-3xl font-bold tracking-tight text-slate-900 leading-tight">
    Modern Developer Continuity
  </h1>
  <p class="mt-3 text-base text-slate-600 leading-relaxed">
    Autonomous agent substrate running on physical hardware with zero telemetry.
  </p>
</div>
```

---

## Verification & Self-Improvement Rubric

Before completing a typography review, verify:
- [ ] No arbitrary pixel values used (`font-size: 17px`, `line-height: 22px`).
- [ ] Large headings (`text-2xl+`) use tight leading (`leading-tight` or `1.15–1.25`) and tight tracking (`tracking-tight`).
- [ ] Body paragraphs have comfortable line-height (`leading-relaxed` or `1.5–1.6`) and line length limited to ~65 characters (`max-w-prose`).
- [ ] Small uppercase badges and tags use `tracking-wider` or `tracking-widest`.

### Self-Improvement Feedback Loop
If you encounter a font that requires custom optical adjustments outside this scale, record a lesson in Sila:
```bash
sila lesson "Typographic scale optical clash" --cause="font family has high x-height requiring looser leading on headers" --fix="override leading-tight to leading-snug for display font"
```
