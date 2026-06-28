# Glossary

## Slot

One execution window for the BLOC protocol path. In this repo, the slot-scoped ACS adapter runs one agreement instance per slot.

## Inclusion List

A bounded, deterministic list of candidate encrypted transactions proposed by an operator for a slot.

## Placeholder

A public transaction or payload wrapper that carries encrypted transaction data before decryption and materialization.

## Ciphertext

The public encrypted representation produced by the BTE library. It includes the BTE capsule, encrypted raw bytes, and integrity metadata.

## BatchPlan

A deterministic sub-batch layout computed from the final ordered ciphertext batch so every operator can generate compatible threshold shares.

## BatchID

A deterministic identifier for the agreed encrypted batch used to scope decryption shares and reconstruction.

## Sub-batch

One partition of a `BatchPlan` used to satisfy BTE index constraints while preserving overall consensus order.

## ACS

Asynchronous Common Subset. The consensus primitive that outputs a common subset of proposer payloads.

## RBC

Reliable Broadcast. The dissemination layer used inside ACS for one proposer payload.

## BBA

Binary Byzantine Agreement. The per-proposer agreement layer used inside ACS to decide whether a proposer payload is accepted.

## Materialized Transaction Set

The final plaintext transaction output recovered after agreement and threshold share combination.

## Trusted-Dealer Config

A prototype-only cluster configuration produced from centrally generated key material rather than DKG.
