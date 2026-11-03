# Super Niuma: two entries, one execution foundation

The traditional Niuma entry remains a conversational workspace. The Super entry is a separate work-order control plane. Shared packages provide execution capabilities; they do not make the two business lifecycles the same.

## Invariants

- Ordinary Session requests never traverse Super admission or start its background services.
- Existing Session schemas, prompts and lifecycle semantics do not acquire work-order fields.
- A work order may reference several isolated execution sessions in a future execution phase; it never commandeers an existing user conversation.
- Intake, execution, verification and delivery are distinct facts. Recording a request does not mean work has started or passed acceptance.
- Super defaults to its own state directory; it does not read old session databases or credentials.
- The public repository must build independently of the former repository after that repository becomes private.

## Current milestone

The first feature slice adds a dedicated, loopback-only intake console; consult the ledger for delivery status. It records explicit human requests with acceptance criteria, supports idempotent intake and cancellation, and reports that execution is disabled. There is deliberately no model call or execution adapter in this milestone.

See ../plans/2026-11-03-super-niuma-evolution.md for the authoritative ordered queue, interfaces, acceptance gates, risks and deferrals.
