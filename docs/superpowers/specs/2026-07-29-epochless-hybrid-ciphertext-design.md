# Epochless Hybrid Ciphertext And Static Campaign Corpus Design

## Objective

Replace BLOC's slot-bound hybrid ciphertext with a minimal epochless
construction and make the final metric campaigns consume immutable,
cluster-specific encrypted transaction corpora through `mempool-il`.

The refactor intentionally replaces the frozen issue #14 candidate. It does
not preserve the source
`2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`, the
`bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`
image, or their ciphertext/configuration contract as the executable baseline
for issue #15. Those identities remain historical evidence.

The replacement candidate retains coordinated index allocation for the initial
prototype and final thesis metrics. Paper-aligned independent index sampling is
deferred until the evidence program is frozen and is tracked by GitHub issue
[#22](https://github.com/VascoMS/bloc/issues/22).

## Design Invariants

- A ciphertext remains valid for the lifetime of the BTE public key and CRS
  used to create it.
- Slot, epoch, block, operational cluster label, expiry, and prior inclusion
  state do not determine ciphertext validity.
- The BTE proof binds the puncture index and capsule components to the public
  setup and public key.
- The hybrid AEAD binds the encrypted transaction bytes to the exact BTE
  capsule.
- A ciphertext index is a puncturable-PRF domain point. It is not a slot,
  transaction ID, block position, operator ID, or globally consumed sequence
  number.
- The same ciphertext may be proposed in multiple slots without re-encryption.
- Inclusion lists, ACS messages, network envelopes, decryption-share
  admission, and node state remain slot-scoped.
- Transaction expiry, replacement, already-included detection, and mempool
  consumption remain outside BTE.
- Primary latency measurements do not include corpus encryption or fixture
  construction.
- Resource measurements remain a separate campaign phase.
- No decoder, runner, or service silently accepts, regenerates, rebuilds, or
  substitutes an incompatible ciphertext, corpus, source, configuration, or
  image identity.

## Paper Correspondence And Index Decision

BEAT-MEV defines encryption as `Enc(pk, m, i)` and includes index `i` in the
ciphertext and the NIZK statement. The index selects the point at which the
underlying PRF key is punctured. It is selected when the client encrypts, before
the final block batch is known.

The paper first presents a coordinated correctness model in which indexes are
unique within a batch. Its practical uncoordinated construction instead has
independent clients sample indexes from the setup domain, accepts collisions,
and deterministically separates equal indexes into different sub-batches.
`alpha` can increase to the maximum observed index multiplicity so collisions
reduce efficiency rather than correctness.

This replacement candidate deliberately keeps the prototype's coordinated
allocation:

```text
index = corpus_position % BMax
```

For an encrypted artifact containing at most `BMax` transactions, this assigns
one distinct index to every ciphertext. The decision provides a stable,
collision-free workload for the current metrics but does not model
permissionless client index selection. Documentation and thesis claims must
state that limitation. No current metric may be presented as evidence about
the paper's random-index collision distribution or targeted-index censorship
behavior.

The refactor retains the existing deterministic collision-aware planner even
though the frozen corpus is collision-free. Equal indexes must still be placed
in different sub-batches, and `alpha` must still rise to at least the maximum
index multiplicity for adversarial and compatibility inputs. Issue #22 will
replace coordinated allocation only after all M5-M7 evidence is complete.

## Minimal Hybrid Ciphertext

The cluster-facing ciphertext becomes:

```go
type Ciphertext struct {
	Capsule     CT
	EncryptedTx []byte
}
```

The lower-level paper-aligned capsule remains:

```go
type CT struct {
	I     int
	Gamma kyber.Point
	Kp    kyber.Point
	C     elgamal.CT
	Pi    Proof
}
```

`CT` therefore contains:

- puncture index `I`;
- punctured PRF key `Kp`;
- masked `GT` message `Gamma`;
- threshold-ElGamal ciphertext `C = (A, B)`; and
- the proof `Pi = (Ap, Bp, Yp, KHat, UHat)`.

The hybrid `GT` message is a random capsule secret rather than the Ethereum
transaction bytes. AES-256-GCM encrypts the arbitrary signed transaction bytes
under a key derived from that secret.

The following current fields and APIs are removed:

- `Ciphertext.Version` as mutable object state;
- `Ciphertext.ClusterID`;
- `Ciphertext.Slot`;
- outer `Ciphertext.Index`;
- separate `Ciphertext.Nonce`;
- `Ciphertext.PlaintextHash`;
- `CT.Context`;
- `CiphertextScope`;
- `PlanBatchFor`;
- `DecodeBatchFor`;
- `DecodeAndPlanBatchFor`; and
- `PlaintextResult.HashOK`.

The wire encoding retains an immutable format discriminator even though version
is not a public struct field.

## Hybrid Key Derivation And Authentication

Encryption proceeds in this order:

1. Sample a random `GT` capsule secret.
2. BTE-encrypt that secret at the caller-selected puncture index.
3. Canonically encode the complete capsule, including its proof.
4. Compute:

   ```text
   capsule_digest = SHA256(canonical_capsule)
   ```

5. Derive the 32-byte AES key with HKDF-SHA256:

   ```text
   input key material = serialized GT capsule secret
   salt = empty
   info = "bloc-hybrid-key-v2" || capsule_digest
   ```

6. Generate a random 12-byte GCM nonce.
7. Encrypt with associated data:

   ```text
   "bloc-hybrid-aad-v2" || capsule_digest
   ```

8. Store:

   ```text
   EncryptedTx = nonce || AES-GCM ciphertext || 16-byte tag
   ```

The capsule digest prevents moving an encrypted payload to a different valid
capsule, including a capsule produced from deliberately reused secret material.
The public key and setup remain bound through the BTE proof rather than through
an operational cluster string.

Decryption never returns unauthenticated plaintext. An incorrect threshold
share subset reconstructs the wrong `GT` secret, derives the wrong AES key, and
fails `AES-GCM.Open`. Bounded invalid-share recovery therefore uses successful
GCM authentication as its subset-validity test.

Ethereum decoding, signature recovery, and campaign-corpus comparison remain
post-decryption application checks. They do not replace GCM authentication.

## Canonical Ciphertext Wire V2

The canonical encoding is a clean `bte-tx-v2` break:

```text
fixed magic and wire version
uint64 capsule length
canonical capsule bytes
uint64 encrypted payload length
nonce || AES-GCM ciphertext || tag
end of input
```

The capsule encoding contains:

```text
int64 puncture index
seven length-prefixed points:
  Gamma, Kp, ElGamal A/B, proof Ap/Bp/Yp
two length-prefixed proof scalars:
  KHat/UHat
```

It no longer contains an application context field.

Decoding rejects:

- legacy `bte-tx-v1`;
- unknown magic or version;
- lengths that overflow or exceed configured input bounds;
- an index outside `[0, BMax)`;
- invalid point or scalar encodings;
- a payload shorter than the 12-byte nonce plus 16-byte GCM tag;
- missing proof components; and
- trailing bytes.

The proof transcript keeps a versioned BTE domain and hashes the public key,
setup/`BMax` domain, index, masked message, punctured key, ElGamal points, and
proof commitments. Removing `CT.Context` must not remove index, public-key, or
setup binding.

`BatchID` becomes:

```text
SHA256(
  "bloc-batch-v2" ||
  for each selected ciphertext in order:
    uint64(length) || canonical_ciphertext
)
```

The same ordered ciphertexts intentionally retain one `BatchID` across slots.
Slot identity remains in the authenticated protocol envelope and fresh
per-slot node state.

## Plaintext And Encrypted Corpus Contracts

The plaintext source is one deterministic master corpus of 512 unique signed
EIP-1559 transactions for development chain ID 1337. Nested prefixes define
the fixed transaction sets:

| Set size | Transfer | 128 B | 256 B | 1 KiB | 4 KiB |
|---:|---:|---:|---:|---:|---:|
| 8 | 2 | 4 | 1 | 1 | 0 |
| 32 | 9 | 16 | 4 | 2 | 1 |
| 128 | 36 | 64 | 15 | 10 | 3 |
| 512 | 143 | 256 | 62 | 41 | 10 |

The master ordering must satisfy all four prefix distributions. Validation
derives transaction class from decoded signed bytes, verifies signatures and
chain ID, requires unique transaction hashes, and hashes each exact ordered
prefix.

Encryption is an offline preparation action:

```text
plaintext master corpus
        |
        v
cluster-specific offline encryption
        |
        v
immutable encrypted-corpus artifact
        |
        v
mempool-il read-only serving
```

The generator uses `index = position % BMax` and refuses an input count greater
than `BMax`. It encrypts every target once, builds the mock placeholder carrier,
verifies every generated proof, decrypts the completed artifact as a
self-check, verifies the expected signed transaction hashes, and writes the
artifact atomically.

Separate encrypted artifacts are mandatory for every distinct BTE public
configuration. The issue #15 primary phase requires at least:

- `n=4`, `t=3`, `BMax=128`; and
- `n=7`, `t=5`, `BMax=128`.

The later extension requires new `BMax=512` configurations and artifacts. A
BMax-128 ciphertext is never reused under a BMax-512 setup even when the
plaintext transaction belongs to both logical prefixes.

## Encrypted-Corpus Manifest

Operational metadata lives once in the encrypted-corpus artifact rather than
inside every ciphertext:

```text
schema_version
ciphertext_wire_version
public_config_id
plaintext_master_corpus_id
plaintext_prefix_set_ids
encrypted_corpus_id
encrypted_prefix_set_ids
bmax
available_count
index_assignment = "coordinated-position-v1"
ordered index schedule
class counts
ordered encrypted candidates
```

`PublicConfigID` is an early-routing and provenance checksum:

```text
SHA256(
  "bloc-bte-public-config-v1" ||
  suite_id ||
  BMax ||
  CRS_SHA256 ||
  canonical_public_key
)
```

It does not add confidentiality and is not part of ciphertext validity. It
detects distribution of a corpus encrypted for the wrong public setup before
ACS.

`EncryptedCorpusID` hashes the ordered canonical ciphertext encodings and
stable candidate metadata. Prefix IDs hash each exact returned prefix. Expected
plaintext hashes remain in the campaign/corpus validation manifest and
materialized results, not in individual ciphertexts or the public inclusion
list response.

## Static Mempool Service

Measured replay mode loads a validated encrypted-corpus artifact and performs
no BTE encryption. It removes the `-replay-slot` behavior and does not maintain
a slot-keyed encryption cache.

The service supports:

```http
GET /inclusion-list?slot=<slot>&limit=<count>
```

`slot` is retained only for request correlation. It does not select or mutate
ciphertexts. `limit` must be positive and no greater than the artifact's
`BMax`. The response contains the first:

```text
min(limit, available_count)
```

candidates plus schema, public-configuration, corpus, prefix, requested-count,
available-count, and returned-count metadata.

Returning fewer candidates than requested is valid generic mempool behavior.
The controlled campaign requires the exact requested occupancy and fails
preflight if fewer items are available.

Repeated requests for the same limit return byte-identical ciphertexts and
ordering across slots. Replay mode deliberately does not consume included
transactions because its purpose is repeatable measurement. A production
mempool's removal, replacement, expiry, and chain-nonce policies are outside
this source.

The endpoint never exposes raw target transaction bytes. It retains mock
placeholder carrier data and encrypted payloads only.

## Node And Evaluator Integration

The slot preparation control becomes:

```go
type prepareSlotRequest struct {
	Slot          uint64 `json:"slot"`
	ProposalLimit int    `json:"proposal_limit,omitempty"`
}
```

`ProposalLimit` belongs to `slotState`; it is not cryptographic configuration.
Zero preserves the ordinary provider maximum. For a positive limit, a
`mempool-http` node requests:

```text
/inclusion-list?slot=<active slot>&limit=<proposal limit>
```

Before ACS, the node validates:

- provider schema and ciphertext wire version;
- response `PublicConfigID` against the ID computed from its loaded setup;
- expected master corpus identity when campaign configuration requires it;
- requested, available, and returned count relationships;
- candidate encoding and existing proposal/envelope bounds; and
- absence of plaintext transaction bytes.

The node accepts fewer than the limit in generic operation. The evaluator and
campaign validator require exact occupancy for controlled cells.

Persistent evaluation passes `scenario.BatchSize` as `ProposalLimit` for every
warmup and measured slot. Batches 8, 32, and 128 can therefore alternate in one
long-lived cluster without node restart, mempool reconfiguration, corpus
mutation, or per-slot encryption.

The direct `/tx` path remains a development and demo path and calls:

```text
EncryptTx(rawTx, index)
```

It retains coordinated local allocation for this candidate. The final
campaigns require `tx_source=mock-placeholder` and must reject a synthetic or
direct fallback.

## Replay And Duplicate Boundary

Slot replay protection remains at the protocol layer:

- `Envelope.Slot`;
- `SlotMessage.Slot`;
- `InclusionList.Slot`;
- `SlotOutput.Slot`;
- active-slot checks before metrics or state mutation;
- fresh ACS and decryption-share state for every prepared slot; and
- authenticated operator identity on network envelopes.

Decryption shares remain scoped by `BatchID`, sub-batch ID, operator identity,
and the outer slot-bound envelope. A mathematically identical share for an
identical batch in a later slot is not a confidentiality or correctness
violation. An untrusted peer cannot impersonate an honest configured peer to
replay that peer's share.

This prototype does not add an execution ledger or production duplicate
transaction filter. Campaign validation intentionally observes the same fixed
plaintext set in many slots and compares each materialized result with the
expected prefix manifest.

## Compatibility And Failure Semantics

This is a clean coordinated upgrade:

- nodes and `mempool-il` are upgraded as one source/image generation;
- cluster configuration advances from `bloc-cluster-v2` to
  `bloc-cluster-v3`;
- `bte-tx-v1` ciphertexts are rejected rather than migrated;
- old replay fixtures and slot-bound caches are rejected;
- missing encrypted artifacts are fatal in measured replay mode;
- public-configuration or corpus mismatch fails proposal preparation before
  ACS;
- malformed selected ciphertexts fail the slot at the existing decode
  boundary;
- invalid capsule proofs fail share generation or combination;
- GCM authentication failure participates in bounded share-subset recovery;
- recovery-budget exhaustion remains a bounded terminal combine failure; and
- no failure path falls back to plaintext, synthetic submissions, legacy
  decoding, fixture regeneration, or a different image.

Failure artifacts retain configuration, corpus identities, requested and
returned counts, slot, stage, bounded reason, node status, and available logs.
They never retain credentials, operator secret material, container
environments, or live-chain keys.

## Twelve-Step Implementation Sequence

1. Add failing BTE tests for the minimal object, v2 wire, epochless reuse,
   capsule/payload binding, legacy rejection, and GCM-based subset recovery.
2. Remove `CT.Context` and its proof/encoding APIs while preserving the paper
   proof statement over setup, public key, index, and ciphertext components.
3. Implement the minimal hybrid object, capsule-digest HKDF/AAD, combined
   nonce/payload/tag representation, and canonical wire v2.
4. Remove plaintext-hash validity from BTE combination and make authenticated
   AEAD success the bounded subset-recovery oracle.
5. Build and validate the deterministic 512-transaction master corpus and all
   four nested prefix identities.
6. Add atomic offline encrypted-corpus generation with coordinated indexes,
   provenance, proof verification, and full decrypt/self-check.
7. Replace slot-keyed replay encryption with immutable artifact loading and
   stateless bounded prefix serving.
8. Update `bloc-node` decoding, provider provenance checks, slot proposal
   limits, materialization, and successive-slot behavior.
9. Update local and remote evaluators and manifests so every scenario controls
   provider occupancy and records source/config/corpus/image identities.
10. Migrate configuration, Compose, EC2 staging, image distribution, and
    side-effect-free `--validate-only` contracts to the new node and mempool
    generation.
11. Update canonical architecture, module, decision, changelog, validation,
    status, and runbook owners; explicitly supersede decision 0012's
    ciphertext-slot binding while retaining protocol slot binding.
12. Run the full local validation and preflight matrix, then freeze one
    replacement source SHA, immutable node and mempool image digests, exact
    corpora, configuration, schema, and validation artifacts before requesting
    separate AWS authorization.

Each implementation task begins with failing tests and ends with focused tests,
the applicable module suite, and a reviewable commit.

## Validation Contract

### BTE

- minimal v2 canonical round trip;
- deterministic re-encoding and golden fixture;
- explicit v1 rejection;
- index bounds and index-mutation proof failure;
- capsule point/scalar/proof mutation rejection;
- nonce, payload, and tag mutation rejection;
- swapping encrypted payloads between capsules fails;
- wrong public key or CRS cannot produce a successful result;
- invalid-share subset recovery succeeds or exhausts its existing bound using
  GCM authentication;
- identical ciphertext and `BatchID` remain valid across simulated slots;
- decoder fuzz targets cover the v2 envelope and capsule; and
- full-path benchmarks report new wire size and stage timings.

### Corpus And Mempool

- exact 512-row master and four prefix distributions;
- unique, signed, recoverable chain-1337 transactions;
- coordinated index schedule is exact, bounded, and recorded;
- encrypted artifact self-check materializes every expected transaction;
- wrong setup, corpus ID, digest, count, or version fails closed;
- arbitrary valid limits return the exact nested prefix;
- repeated requests and different slot parameters return byte-identical
  ciphertext sequences;
- invalid limits are rejected;
- insufficient availability is reported without fabrication; and
- API responses contain no plaintext target bytes.

### Node And Evaluator

- per-slot proposal limit reaches the provider request;
- generic operation accepts up to the limit;
- campaign validation requires exact occupancy;
- provider/setup/corpus mismatches fail before ACS;
- the same encrypted prefix completes in successive slots;
- stale envelopes and wrong-slot inclusion lists remain rejected;
- all correct nodes report identical materialized transaction hashes and
  ordering;
- scheduled persistent scenarios retain the expected limit and identities;
- synthetic fallback is rejected for final-campaign mode; and
- latency timing excludes offline corpus encryption and cluster startup.

### Full Local Gate

```sh
cd bte/btd-impl-main && go test ./...
cd mempool-il && go test ./...
cd bloc-node && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

The gate also includes targeted race suites, both BTE decoder fuzz targets,
hybrid full-path benchmarks, runner portability tests, Compose
mock-placeholder rehearsal, campaign `--validate-only`, and a local persistent
`n=4/7`, `BMax=128`, batch `8/32/128` preflight.

No AWS API, Terraform plan/apply, ECR push, or billable resource is part of
this validation gate.

## Replacement Freeze And Campaign Checkpoints

1. **Frozen provenance readiness:** all local validation passes; source,
   binaries/images, configuration, CRS, plaintext corpus, encrypted corpora,
   schemas, and commands are immutable and checksummed.
2. **Primary n=4 pilot/readiness:** the exact frozen artifacts pass
   side-effect-free runner validation and the authorized short live pilot
   before a long launch.
3. **Primary n=4/7 p99 collection:** only the six `BMax=128` primary cells use
   10 warmups, 1,000 measured attempts, 10 balanced blocks, seed `20260621`,
   and the 12-second completion boundary.
4. **Separate resource collection:** CPU, memory, network, restart, and OOM
   evidence is collected in a dedicated non-latency phase.
5. **Extension pilot decision:** `n=10` and batch 512 remain a later
   30-observation pilot/continuation decision, not part of the primary launch.
6. **Validation, analysis, cleanup, and documentation:** accept or reject
   artifacts against the shared final-campaign contract, verify authenticated
   cleanup, retain failures, and update canonical evidence owners.

AWS execution remains separately authorized after the replacement freeze and
no-AWS readiness review.

## Documentation And Tracking

Implementation updates the existing canonical owners:

- `docs/ARCHITECTURE.md`;
- `docs/modules/bte.md`;
- `docs/modules/bloc-node.md`;
- `docs/modules/mempool-il.md`;
- `docs/DECISIONS.md`;
- `docs/CHANGELOG.md`;
- `docs/VALIDATION.md`;
- `docs/STATUS.md`;
- `bte/btd-impl-main/README.md` when public APIs change;
- `mempool-il/README.md`;
- `bloc-node/README.md`;
- `deploy/docker-compose/README.md`;
- `deploy/ec2/README.md`; and
- issue #15 for the replacement candidate and campaign readiness.

Issue #22 remains Deferred/Backlog/Low/BTE and is not implemented on the issue
#15 branch.

## Non-Goals

- No uncoordinated or randomly sampled index allocation in the current
  candidate.
- No evidence claim about random collision frequency or index-censorship
  resistance.
- No production mempool consumption, execution ledger, replacement policy, or
  duplicate-inclusion prevention.
- No public decryption-share proofs, secure DKG, production CRS ceremony,
  cryptographic common coin, Builder API, DVT signing, execution payload
  construction, or block publication.
- No dual ciphertext decoder or mixed-version network.
- No preservation of old `BatchID`, ciphertext-size, configuration, corpus, or
  image identities.
- No reuse of measurements collected from different source, image, corpus,
  setup, schema, or index-semantics generations.
- No AWS execution without separate explicit authorization.
