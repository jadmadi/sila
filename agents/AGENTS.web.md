# AGENTS.web.md: Web Application and Frontend Lifecycle Standards

This document establishes client-side state, persistence, and external handoff
lifecycle rules across web applications.

## 1. External Handoff and State Persistence Guards

- Persistence confirmation before unlock: external handoffs, export triggers,
  and share intents must only unlock after confirming database persistence.
- Never offer a share or handoff action for unsaved, draft, or uncommitted state.
- Never redirect away from or unload a view during an uncompleted external
  handoff or in-flight background sync.
- Optimistic UI boundary: optimistic UI updates must roll back cleanly and
  notify the user if persistent storage rejects the mutation.

## 2. Browser Navigation and View Lifecycle

- Navigation guards: prompt the user with a confirmation barrier before view
  navigation if unsaved input or pending uploads exist.
- Clean tear-down: abort active Fetch requests and disconnect WebSocket streams
  in component unmount cleanups to prevent resource leaks.
