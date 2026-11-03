---
name: refactor-ui
description: Orchestrate a comprehensive multi-pass visual design audit across all 10 Refactoring UI principles, producing prioritized findings and code transformations. Use when reviewing an entire page, redesigning complex screens, conducting UI quality audits, or elevating developer-built interfaces to production grade.
domain: ui-design
---

# Refactor UI (Meta-Orchestrator)

## Overview

The `refactor-ui` orchestrator executes a systematic, 5-phase visual design audit across any interface, component, or screen. Rather than offering scattered subjective opinions, it evaluates the user interface against the 10 atomic *Refactoring UI* principles, pinpoints structural anti-patterns, and produces prioritized, copy-pasteable code transformations.

> *Attribution*: Derived from the *Refactoring UI* methodology by Adam Wathan & Steve Schoger.

---

## Trigger Scenarios

Activate this orchestrator when:
- Elevating a "developer UI" or prototype into a polished, high-end production application.
- Conducting a comprehensive UI/UX code review of a frontend view, page, or layout.
- Preparing a feature for public release where visual craftsmanship is paramount.
- Users report that an interface feels "cluttered", "amateurish", "noisy", or "hard to scan".
- Auditing design token consistency across Tailwind CSS, CSS modules, or inline styles.

---

## The 5-Phase Audit Pipeline

```
  Phase 1: Architecture & Focus  ───►  [establish-visual-hierarchy] + [eliminate-visual-clutter]
           │
  Phase 2: Spatial Rhythm        ───►  [group-related-elements] + [apply-consistent-spacing]
           │
  Phase 3: Typographic Systems   ───►  [apply-typography-scale] + [design-empty-states]
           │
  Phase 4: Color, Light & Depth  ───►  [build-color-palette] + [manage-color-contrast] + [use-shadows-appropriately]
           │
  Phase 5: Action Affordances    ───►  [design-button-hierarchy]
```

### Phase 1: Architecture & Focal Points
1. **Visual Hierarchy Check**: Does each container have exactly one primary focal point? Are secondary elements de-emphasized using lighter color and normal weight rather than inflated header sizes?
2. **Clutter Elimination Check**: Are there heavy borders or dividers that can be replaced with background color shifts, subtle alternating fills, or whitespace?

### Phase 2: Spatial Rhythm & Grouping
3. **Gestalt Proximity Check**: Is the gap between distinct groups at least 2.5×–3× larger than intra-group spacing (e.g. label-to-input gap)?
4. **Spacing Scale Check**: Are all margins, paddings, and gaps locked to an 8pt/4pt fixed scale (`4, 8, 12, 16, 24, 32, 48, 64px`)? Are arbitrary pixel values (`mt-[13px]`, `p-[17px]`) eradicated?

### Phase 3: Typographic Scales & Content States
5. **Typography Scale Check**: Are font sizes constrained to a handcrafted modular scale? Are large headings tightened (`leading-tight`, negative letter-spacing) and long body paragraphs loosened (`leading-relaxed`)?
6. **Zero-Data State Check**: If data is missing or empty, does the screen present an empathetic, actionable empty state with an illustration and primary CTA, or does it leave an awkward blank void?

### Phase 4: Color, Light & Depth
7. **Color Ramp Check**: Does the palette use a disciplined 9-shade ramp (50–900) with warm/cool neutrals rather than uncurated hex codes?
8. **Contrast & Tinting Check**: Does body copy achieve WCAG 2.2 AA (≥ 4.5:1)? Is harsh pure black (`#000000`) on white avoided? Are grays tinted to match saturated background canvases?
9. **Shadow & Elevation Check**: Are shadows constructed with layered ambient + direct light rather than muddy single-layer black drop shadows? Does dark mode express elevation through surface lightness tiers?

### Phase 5: Action Affordances
10. **Button Hierarchy Check**: Is there strictly one primary solid button per visual scope? Are secondary actions styled with outlines/soft fills and destructive/passive actions with ghost text?

---

## Severity Classification Matrix

| Severity | Definition | Examples | Action Required |
| :--- | :--- | :--- | :--- |
| 🔴 **Critical** | Breaks usability, legibility, or core workflow | Text contrast < 3:1; multiple competing primary CTAs; missing empty state leaving page broken | Fix immediately before shipping |
| 🟠 **High** | Severe visual clutter, chaotic hierarchy, or bad spacing | Heavy wireframe borders everywhere; label-input spacing collision; unscaled typography | Refactor layout structure and spacing |
| 🟡 **Medium** | Inconsistent token usage, sub-optimal depth | Arbitrary margins (`mt-3.5`); harsh pure-black shadows; uncalibrated line heights | Align to design token scales |
| 🟢 **Low** | Micro-polish and refinement | Tinting secondary text toward background hue; subtle hover transition timing | Apply during polishing pass |

---

## Standard Audit Output Schema

When running the orchestrator, generate findings using this structured markdown format:

```markdown
# Refactoring UI Audit Report: [Page / Component Name]

## Executive Summary
- **Overall Craft Score**: [e.g. 72 / 100]
- **Key Finding**: [One-sentence diagnostic summary]
- **Primary Levers**: [Top 2 skills to execute, e.g. `eliminate-visual-clutter` + `establish-visual-hierarchy`]

## Audit Findings by Priority

### 🔴 Critical: [Title]
- **Principle**: [Name of Atomic Skill]
- **Location**: `src/components/InvoiceCard.tsx:42`
- **Issue**: [Concrete description of the defect]
- **Remedy**: [Exact refactoring heuristic to apply]

### 🟠 High: [Title]
...

## Refactored Code Implementation
[Unified code snippet showing the transformed component with all 10 principles applied]
```

---

## Verification & Self-Improvement Rubric

Before completing an audit or presenting refactored UI code:
- [ ] All 5 phases of the audit pipeline were evaluated systematically.
- [ ] Findings are categorized with clear severity levels (Critical, High, Medium, Low).
- [ ] Refactored code includes zero arbitrary pixel values and adheres to standard 4pt/8pt spacing.
- [ ] Refactored code achieves 100% WCAG 2.2 AA contrast compliance.
- [ ] Exactly one primary button exists within the visual view.

### Self-Improvement Feedback Loop
When completing an audit where an unusual framework or edge condition was encountered, persist the finding into Sila's knowledge store:
```bash
sila lesson "Complex data table refactoring pattern" --cause="table required 14 columns causing horizontal blowup" --fix="applied sticky frozen columns with subtle right-edge ambient shadow"
```
