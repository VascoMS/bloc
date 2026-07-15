# BLOC Thesis Prototype

This repository contains the BLOC thesis prototype and its supporting modules for evaluating encrypted transaction handling, slot-scoped multi-operator agreement, and threshold decryption in an Ethereum-compatible setting.

## Project Overview

The repository is organized into five main components:

- `bloc-node/`: integrated prototype node, local evaluator, transport layers, and reporting tools
- `mempool-il/`: standalone mempool inclusion-list service
- `bte/btd-impl-main/`: BEAT-MEV batched threshold encryption library and benchmarks
- `sbc/hbbft/`: HoneyBadger ACS implementation adapted for slot-scoped BLOC use
- `latency-charts/`: static SVG/PNG chart generation for evaluator latency results

Supporting material lives in:

- `papers/`: research PDFs and reference material
- `results/`: generated evaluation artifacts and local experiment output

## Current Project State

For the current implementation status, active milestone, next steps, and last known good baseline, see [docs/STATUS.md](/bloc/docs/STATUS.md).

## Documentation Guide

Start here depending on what you need:

- System design and protocol boundaries: [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- Protocol implementation deep dives: [docs/modules/](/bloc/docs/modules/)
- Current status and next development step: [docs/STATUS.md](/bloc/docs/STATUS.md)
- Validation commands and evidence model: [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)
- Roadmap and milestone sequence: [docs/ROADMAP.md](/bloc/docs/ROADMAP.md)
- Developer process and workflow: [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- Key design rationale: [docs/DECISIONS.md](/bloc/docs/DECISIONS.md)
- Shared terminology: [docs/GLOSSARY.md](/bloc/docs/GLOSSARY.md)

If you are using Codex or another agent, agent-specific instructions live in [AGENTS.md](/bloc/AGENTS.md).

## Module Entry Points

- [bloc-node/README.md](/bloc/bloc-node/README.md)
- [mempool-il/README.md](/bloc/mempool-il/README.md)
- [bte/btd-impl-main/README.md](/bloc/bte/btd-impl-main/README.md)
- [sbc/hbbft/README.md](/bloc/sbc/hbbft/README.md)
- [latency-charts/README.md](/bloc/latency-charts/README.md)

## Quick Validation

Run module tests from the relevant module root:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

For the standard local prototype smoke flow and experiment paths, see [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md) and [docs/VALIDATION.md](/bloc/docs/VALIDATION.md).
