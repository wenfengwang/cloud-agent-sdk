# demo/design/ — Demo Design Documents

This directory owns the architecture, contracts, and rationale of the [demo sub-project](../AGENTS.md).

Follow the [root design rules](../../design/AGENTS.md) (files, status lifecycle, writing rules) and start every design from the [root template](../../design/000-template.md). Neither is copied here.

## Local Rules

- **Own numbering.** Demo designs are `demo/design/NNN-slug.md`, numbered from `001` independently of the root `design/`. A number is never reused. Inside these documents, `related` lists demo design numbers only.
- **Link to the SDK contract; do not restate it.** SDK behavior (signatures, turn lifecycle, message and tool types, errors, harness behavior, non-goals) is owned by [design/001-core-api.md](../../design/001-core-api.md) and SDK testing by [design/002-testing.md](../../design/002-testing.md). A demo design states only how the demo uses that contract and links to the owning section.
- **Links run one way by default.** Demo designs link to root designs. A root design may link to a demo design as an example consumer, but never depends on it for a contract.
- **Gaps found here go upstream as links.** When the demo exposes an SDK gap, record it in the [gap registry](001-architecture.md#sdk-gaps). Changing the SDK to close a gap is a root design change, not a demo change.
