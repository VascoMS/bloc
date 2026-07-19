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

For the current implementation status, active milestone, next steps, and last known good baseline, see [docs/STATUS.md](docs/STATUS.md).

## Documentation Guide

Start here depending on what you need:

- System design and protocol boundaries: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- Protocol implementation deep dives: [docs/modules/](docs/modules/)
- Current status and next development step: [docs/STATUS.md](docs/STATUS.md)
- Validation commands and evidence model: [docs/VALIDATION.md](docs/VALIDATION.md)
- Roadmap and milestone sequence: [docs/ROADMAP.md](docs/ROADMAP.md)
- Developer process and workflow: [docs/WORKFLOWS.md](docs/WORKFLOWS.md)
- Local Compose operation: [deploy/docker-compose/README.md](deploy/docker-compose/README.md)
- VM/EC2 operation and campaigns: [deploy/ec2/README.md](deploy/ec2/README.md)
- Key design rationale: [docs/DECISIONS.md](docs/DECISIONS.md)
- Shared terminology: [docs/GLOSSARY.md](docs/GLOSSARY.md)

If you are using Codex or another agent, agent-specific instructions live in [AGENTS.md](AGENTS.md).

## Module Entry Points

- [bloc-node/README.md](bloc-node/README.md)
- [mempool-il/README.md](mempool-il/README.md)
- [bte/btd-impl-main/README.md](bte/btd-impl-main/README.md)
- [sbc/hbbft/README.md](sbc/hbbft/README.md)
- [latency-charts/README.md](latency-charts/README.md)
- [deploy/docker-compose/README.md](deploy/docker-compose/README.md)
- [deploy/ec2/README.md](deploy/ec2/README.md)

## Quick Validation

Run module tests from the relevant module root:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

For the standard local prototype smoke flow and experiment paths, see [docs/WORKFLOWS.md](docs/WORKFLOWS.md) and [docs/VALIDATION.md](docs/VALIDATION.md).
