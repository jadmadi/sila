# AGENTS.ui.md — Frontend, UI, RTL & Design Standards

This document establishes the UI component engineering, Arabic typography,
RTL layout, and design token standards across Jad Madi's projects.

---

## 1. RTL & Arabic-First Principles (Default, Not Afterthought)

- **Logical CSS properties only**:
  - Use `ms-*` / `me-*` (margin-inline-start/end), `ps-*` / `pe-*`
    (padding-inline-start/end), `start-*` / `end-*` (inset-inline-start/end),
    `text-start` / `text-end`.
  - NEVER use directional physical properties (`ml-*`, `mr-*`, `pl-*`, `pr-*`,
    `left-*`, `right-*`, `text-left`, `text-right`) without explicit LTR isolation.
- **Directional icons**: Arrow, chevron, and pagination icons MUST flip in RTL
  (`rtl:rotate-180` or bidirectional icon primitives). Media playback icons do not flip.
- **Punctuation & BiDi isolation**: Wrap phone numbers, email addresses, currency codes,
  code snippets, and Latin brand names in `<bdi>` or `dir="ltr"`.
- **Arabic typography**: Primary fonts must be Noto Kufi Arabic, IBM Plex Sans Arabic,
  Tajawal, or Cairo with relaxed line-heights (`leading-relaxed` / `leading-loose`) to
  accommodate Arabic diacritics.
- **Numerals and calendars**: Default to Western Arabic numerals (0-9); never use
  Eastern Arabic-Indic numerals (٠-٩). Where a Hijri date matters (Islamic events),
  display it alongside the Gregorian date.

---

## 2. React Performance & Component Architecture

- **Outer-Consumer / Inner-Memo pattern for Context**:
  - Subscribing large card lists (`MenuItemCard`, `AddressCard`, `RowItem`) directly to
    broad Context providers causes massive cascaded re-renders.
  - Split into an **Outer Context Consumer** (which extracts only the specific item by ID)
    and an **Inner `React.memo` Component** that receives strictly primitive props.
- **Deterministic state machines over `useEffect` cascades**:
  - Model complex interactive state (multi-step forms, modals, checkout flows) as pure
    deterministic state transitions `(State, Action) => NextState`.
  - Prohibit chaining multiple `useEffect` hooks to synchronize dependent state.
- **Primitives over re-invention**:
  - Use primitive UI component libraries (`smooth-ui`, `hasan-ui`, Radix, shadcn/ui)
    rather than custom-building accessible components from scratch.
  - Brand-locked design tokens: use semantic CSS variables (`var(--primary)`,
    `var(--brand)`) for palette, spacing, and radius. Never hardcode arbitrary
    Tailwind color classes (e.g. `bg-teal-600`).

---

## 3. Visual Identity & Design System Specification

- **Root `DESIGN.md` Standard**:
  - User-facing frontend apps maintain a root `DESIGN.md` following the
    `@google/design.md` standard.
  - Machine-readable tokens (palette, typography, spacing, radius) live in the YAML
    front matter; design rationale, RTL constraints, and component rules live in the
    markdown body.
  - Run `aubx @google/design.md lint DESIGN.md` (or `pnpm dlx @google/design.md lint DESIGN.md`)
    to verify WCAG AA contrast ratios and token references during design updates.

---

## 4. Semantic HTML, Forms & Native Platform Standards

- **Semantic landmark tags**: Always use `<main>`, `<nav>`, `<article>`, `<header>`,
  `<footer>`, `<section>` and native interactive elements (`<dialog>`, `<details>`,
  `<summary>`).
- **Form grouping & semantics**: Group related inputs (radio/checkbox sets, address fields,
  date ranges) inside `<fieldset>` with an explicit `<legend>` descriptor.
- **Labeling**: Pair every `<input>` with an explicit `<label for="...">` or wrap it directly.
- **Native interactions & validation**: Use `<dialog>` for modals and the native `popover` /
  `popovertarget` attributes for popovers/tooltips. Rely on HTML5 constraints (`required`,
  `pattern`) and modern `:user-valid` / `:user-invalid` CSS pseudo-classes.
- **Empty states & loading skeletons**: Every list, search view, and dashboard must provide
  deliberate skeleton loading and empty state designs.

---

## 5. State Persistence & Handoff Lifecycle Guards

- Persistence confirmation before unlock: external handoffs, export triggers,
  and share intents must only unlock after confirming database persistence.
- Never offer a share or handoff action for unsaved, draft, or uncommitted state.
- Never redirect away from or unload a view during an uncompleted external
  handoff or in-flight background sync.
