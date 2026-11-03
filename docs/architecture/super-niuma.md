# Super Niuma: one Desktop, isolated business lifecycles

The traditional Niuma entry remains a conversational workspace. The Super entry is a separate work-order control plane. Shared packages provide execution capabilities; they do not make the two business lifecycles the same.

## Invariants

- Ordinary Session requests never traverse Super admission or start its background services.
- Existing Session schemas, prompts and lifecycle semantics do not acquire work-order fields.
- A work order may reference several isolated execution sessions in a future execution phase; it never commandeers an existing user conversation.
- Intake, execution, verification and delivery are distinct facts. Recording a request does not mean work has started or passed acceptance.
- Super defaults to its own state directory; it does not read old session databases or credentials.
- The public repository must build independently of the former repository after that repository becomes private.

## Desktop composition boundary

Desktop is the primary application. **新建会话** and **超级牛马干活** are adjacent upper-left sidebar actions within the same window, not two applications. Super renders a native React pane through Wails; no iframe, browser launch or additional localhost listener is involved. The legacy selected conversation stays mounted while hidden, preserving its local draft and execution context. Returning to a conversation or opening New Session restores the normal view. Super form drafts stay in memory while switching panes; persisted work orders survive restart.

The only reviewed legacy changes are Desktop composition/lifecycle, React shell navigation, sidebar action, and optional bridge type declarations. Session routing, APIs, schemas, hooks, runtime and providers remain snapshot-protected. The baseline manifest is immutable; a separate review registry pins exact hashes for these four composition files. A changed hash, unknown exception or modified protected file fails the boundary guard.

The Super bridge initializes its own SQLite store lazily, serializes bounded operations, sanitizes errors and drains/closes on Desktop shutdown. It reuses the existing work-order service, including transactional idempotency and cancellation. Intake does not need a model, so Desktop onboarding offers a work-order path without bypassing model setup for conversations.

## Current milestone

The first feature slice adds a dedicated, loopback-only intake console; consult the ledger for delivery status. It records explicit human requests with acceptance criteria, supports idempotent intake and cancellation, and reports that execution is disabled. There is deliberately no model call or execution adapter in this milestone.

See ../plans/2026-11-03-super-niuma-evolution.md for the authoritative ordered queue, interfaces, acceptance gates, risks and deferrals.
