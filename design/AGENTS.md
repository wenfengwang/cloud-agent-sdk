# design/ — Design Documents

This directory owns architecture, behavior contracts, design rationale, and invariants. Repository-wide rules stay in [the root instructions](../AGENTS.md).

## Files

- One design per file, flat: `design/NNN-slug.md` with a zero-padded, never-reused number.
- Start every new design from [000-template.md](000-template.md). Keep its frontmatter fields and required sections.

## Status Lifecycle

| Status | Meaning |
|---|---|
| `draft` | Under discussion; not yet implemented. May contain `Open Questions`. |
| `partial` | Implementation started; `Status` names what is and is not built yet. |
| `current` | Fully implemented and matches the code. `Open Questions` is removed. |
| `superseded` | Replaced; `Status` links the successor, which sets `supersedes`. |

Update `last_verified` whenever you check the document against the code.

## Writing Rules

- Write current behavior and durable rationale. No revision history, change logs, review narration, PR chronology, or status the code can answer.
- When revising, edit the document into the shape it would have had if written correctly the first time. A reader must not be able to tell it was reworked.
- Give each fact one home. Link to the owning design instead of copying its rules.
- `Invariants` lists exactly what cannot change without updating the document. Code that breaks an invariant must change the design in the same change.
- Prefer tables for decisions and mappings, and short prose for reasons. Cut anything a reader does not need to implement or review the design.
