# Batched Threshold Encryption Implementation Architecture

## Responsibility And Non-Goals

`bte/btd-impl-main` is the repository's BEAT-MEV-derived batched threshold
encryption implementation plus a cluster-facing Go API for raw transaction
bytes. It owns BTE setup, hybrid encryption, canonical ciphertext encoding,
scope validation, deterministic batch planning, decryption-share generation,
threshold reconstruction, AEAD opening, and plaintext-integrity checking.

The library does not run consensus, select transactions, transport shares,
validate Ethereum semantics, construct execution payloads, or manage operator
identity. `bloc-node` supplies the consensus-fixed ciphertext order and active
cluster/slot scope.

The code is a research prototype, not production cryptography. The concrete
setup and key-distribution shortcuts described below do not realize the
paper's setup and key-custody assumptions.

## Source Map

| Layer | Principal source and symbols |
| --- | --- |
| Pairing suite adapter | [`curves/curves.go`](/bloc/bte/btd-impl-main/curves/curves.go): `Suite`, `GTBase`, `PickGT` |
| Puncturable PRF | [`prf/prf.go`](/bloc/bte/btd-impl-main/prf/prf.go): setup, puncture, direct/punctured/exponential evaluation |
| Threshold ElGamal | [`elgamal/elgamal.go`](/bloc/bte/btd-impl-main/elgamal/elgamal.go): key generation, additive ciphertexts, partial decryption, interpolation |
| BTD construction | [`be/btd.go`](/bloc/bte/btd-impl-main/be/btd.go): `BTD`, `CT`, `Proof`, `EncWithContext`, `VerifyCT`, batch decryption/combine |
| Cluster-facing API | [`be/cluster.go`](/bloc/bte/btd-impl-main/be/cluster.go): `ClusterBTE`, `Ciphertext`, `DecodedBatch`, `BatchPlan`, `DecryptionShare` |
| Integrated tests/benchmarks | [`be/cluster_test.go`](/bloc/bte/btd-impl-main/be/cluster_test.go) |
| Inherited benchmark harness | [`main_test.go`](/bloc/bte/btd-impl-main/main_test.go), [`bench.sh`](/bloc/bte/btd-impl-main/bench.sh) |

## Public Types And Boundaries

### Core types

- `BTD` owns the pairing suite, PRF setup, threshold ElGamal object, maximum
  batch/index domain `B`, and committee threshold parameters.
- `CT` is the group-valued BTE capsule: puncture index, padded `GT` message,
  punctured key, ElGamal ciphertext, proof, and application context.
- `Ciphertext` adds version, cluster, slot, outer index, AES-GCM nonce and
  ciphertext, and SHA-256 plaintext commitment around a `CT`.
- `DecodedBatch` privately owns parsed ciphertexts, the `BatchID` computed from
  accepted canonical bytes, and optional application scope.
- `BatchPlan` exposes `BatchID`, `Alpha`, and ordered `BatchItem` sub-batches.
- `DecryptionShare` exposes operator ID, `BatchID`, sub-batch ID, and a Kyber
  public share.

### Construction APIs

`NewClusterBTE` is the trusted-dealer/test convenience constructor and retains
all supplied secret shares. `NewNode` creates a node-local object with one
secret-share wrapper and explicitly sets the ElGamal committee size and
threshold.

Both constructors currently set `PublicParams.N` to `BMax`; validation uses it
as the ciphertext index-domain size rather than the operator count. The actual
operator count used by interpolation is stored in the lower-level BTD/ElGamal
state. The naming is misleading and tracked as a review finding.

### Scoped and unscoped APIs

Generic callers may use:

- `PlanBatch`
- `DecodeBatch`
- `DecodeAndPlanBatch`

Application callers that know their runtime context may use:

- `PlanBatchFor(ciphertexts, CiphertextScope)`
- `DecodeBatchFor(encoded, CiphertextScope)`
- `DecodeAndPlanBatchFor(encoded, CiphertextScope)`

`bloc-node` uses `DecodeBatchFor`. A scope contains only cluster ID and slot;
index is validated against the BTE domain and against the inner capsule.

## Cryptographic Data Flow

### 1. Pairing groups and setup

The suite wraps Kyber's Kilic BLS12-381 implementation. The repository swaps
the paper's source-group roles because operations in `G1` are faster in this
backend. Threshold ElGamal and proof commitments use `G1`; puncturable PRF
support uses `G1`, `G2`, and the pairing target `GT`.

PRF setup samples per-index scalars `xi[i]` and `zi[i]`, builds the group
elements needed by puncture and evaluation, serializes only public curve points
with `MarshalPublicBinary`, and discards the generating scalars. The versioned
artifact records suite identity, `BMax`, `G1xi`, `g2zi`, and the row-major
`BMax * BMax` cross-index table. `PRFSetupFromPublic` rejects the wrong version,
suite, domain, malformed points, truncation, and trailing bytes.

Production `bloc-node` and replay-placeholder callers load this artifact via
`NewBTDFromPublicCRS`; seeded constructors remain for generic compatibility.
Removing the shared seed prevents nodes from recovering the setup scalars, but
this is only a partial remediation: the artifact intentionally retains `j == i`
elements that the inherited source says must not be published in a real setup.
It is still prototype trusted setup, not a secure BEAT-MEV CRS.

Threshold ElGamal `KeyGen(n,t)` samples one master scalar, creates a degree
`t-1` Shamir polynomial, returns `n` private shares, and commits the polynomial
to obtain the public key. The trusted generator writes the public key to
`cluster.json` and one share to each `secrets/operator-<id>.json`; a node rejects
a secret whose cluster or operator identity does not match.

### 2. BTE capsule encryption

`BTD.EncWithContext(pk, i, m, context)`:

1. samples PRF key `k`;
2. computes punctured key `Kp` for index `i`;
3. computes `K = g1^k` and threshold-ElGamal encrypts `K`;
4. evaluates the PRF at `i` and adds that pad to message `m` in `GT`;
5. constructs a Schnorr-like proof tying the punctured key and ElGamal
   ciphertext to the same `k` and encryption randomness; and
6. Fiat-Shamir hashes the BTE domain/`BMax`, public key, proof commitments,
   index, application context, padded message, punctured key, and ElGamal
   points.

`VerifyCT` checks index bounds and the three proof equations. Proof verification
is deferred until share generation or batch combine; planning validates
metadata and structure but not the proof equations.

### 3. Hybrid transaction encryption

`ClusterBTE.EncryptTx(rawTx, index, clusterID, slot)` bridges raw byte strings
to the group-valued construction:

1. sample a random `GT` capsule secret;
2. build application context from library version, cluster ID, slot, and index;
3. derive a 32-byte key with HKDF-SHA256 over the serialized `GT` secret;
4. encrypt raw bytes with AES-256-GCM and a random 12-byte nonce;
5. BTE-encrypt the `GT` capsule secret with the same context in the proof; and
6. commit `SHA256(rawTx)` in the outer ciphertext.

The AEAD associated data is:

```text
LibraryVersion || 0x00 || ClusterID || 0x00 || uint64(slot) || int64(index)
```

Integers use big-endian encoding. Changing cluster, slot, or index invalidates
both the GCM authentication and BTE proof transcript.

## Canonical Wire Encoding

### Outer ciphertext

`Ciphertext.MarshalBinary` emits, in order:

1. NUL-terminated version string;
2. NUL-terminated cluster ID;
3. big-endian `uint64` slot;
4. big-endian `int64` outer index;
5. `uint64` length plus nonce bytes;
6. `uint64` length plus encrypted transaction bytes;
7. 32-byte plaintext hash; and
8. `uint64` length plus encoded BTE capsule.

The suite ID is not encoded. Version `bte-tx-v1`, the 12-byte GCM nonce, a
payload of at least the 16-byte GCM tag, index domain, context equality, and
absence of trailing bytes are validated during decoding.

### Capsule

`CT.MarshalBinary` emits:

1. big-endian `int64` puncture index;
2. length-prefixed context;
3. seven length-prefixed points: `Gamma`, `Kp`, ElGamal `A/B`, and proof
   `Ap/Bp/Yp`; and
4. two length-prefixed proof scalars: `KHat/UHat`.

`UnmarshalCT` reconstructs each element using the initialized suite and rejects
invalid point/scalar encodings and trailing bytes. It does not independently
validate the index; outer decoding and planning do.

This Go encoding is deterministic for the current suite but is still labeled a
prototype format. It has no formal cross-language schema or independent suite
negotiation.

## Decoding, Identity, And Ownership

`DecodeBatch` and `DecodeBatchFor` reject `len(encoded) > BMax` before parsing
any item. They decode sequentially and return the first indexed error. Empty
input decodes successfully, allowing `bloc-node` to take its explicit empty-set
completion path; planning an empty decoded batch remains an error.

Scoped decoding validates expected cluster and slot before expensive capsule
decoding when possible, then revalidates the fully reconstructed object.
Validation covers:

- supported outer version;
- cluster and slot scope when present;
- outer index in `[0,BMax)`;
- inner capsule index equal to outer index;
- capsule context exactly equal to the expected AEAD context;
- standard GCM nonce and minimum payload shape; and
- canonical end-of-input for both encodings.

After every item succeeds, `DecodedBatch` calculates `BatchID` directly from
the accepted encoded slices and does not retain those slices. `Ciphertexts()`
deep-copies byte slices and every Kyber point/scalar. Later source or caller
mutation cannot alter the batch identity or subsequent planning.

`BatchID` is:

```text
SHA256(
  LibraryVersion ||
  for each ciphertext in selected order:
    uint64(len(canonicalEncoding)) || canonicalEncoding
)
```

The batch size itself is represented by the number of length-prefixed entries;
there is no separate count field in the hash preimage.

## Deterministic Batch Planning

Planning first validates every in-memory ciphertext, which protects direct
`PlanBatch` callers that bypass deserialization. It counts each puncture index
and chooses:

```text
alpha = ceil(2 * sqrt(B))
alpha = max(alpha, maximum index frequency)
alpha = min(alpha, B)
```

This is the integrated BEAT-MEV Opt-2 starting point. Items are stably sorted by
index frequency descending and original consensus position ascending. The
first layout assigns sorted items round-robin to sub-batches.

If every sub-batch has distinct indices, that layout is returned unchanged. If
a collision exists, the fallback processes the same sorted sequence and assigns
each item to the least-loaded sub-batch that has not seen its index. Equal-load
ties select the lowest sub-batch ID. Because `alpha` is at least the maximum
frequency, an eligible sub-batch should always exist. The fallback result is
validated before return.

Every `BatchItem` retains its original selected position. Sub-batch processing
may reorder work, but combine writes results back to these positions.

## Share Generation And Combination

### Share generation

`MakeShare(secret, plan, subBatchID)` extracts the sub-batch capsules and calls
`BatchDecWithShare` with proof verification enabled. The lower layer:

1. rejects more than `BMax` capsules and duplicate puncture indices;
2. verifies every BTE proof;
3. homomorphically adds the threshold-ElGamal ciphertexts; and
4. multiplies the aggregate ElGamal `A` point by the operator's Shamir share.

The returned share is tagged with application operator ID, `BatchID`, and
sub-batch ID. There is no public proof that the partial decryption share is
correct.

### Candidate-share validation

`CombineShares` rejects a candidate whose `BatchID` differs, whose sub-batch ID
is out of range, or whose application operator ID is duplicated within a
sub-batch. It requires at least `t` candidates for every sub-batch and sorts
Kyber shares by their interpolation index.

The library does not know cluster membership and does not bind application
operator ID to the Kyber share index. That responsibility currently sits at the
caller boundary and is incomplete in `bloc-node`.

### Threshold reconstruction

Because individual shares are not verifiable, `combineSubBatch` enumerates
threshold-sized subsets until one reconstructs valid capsule secrets and all
AEAD/plaintext-hash checks succeed. For each candidate subset:

1. interpolate the aggregate threshold-ElGamal secret;
2. for each capsule, evaluate the aggregate PRF and all other punctured keys;
3. remove the BTE pad to recover the `GT` capsule secret;
4. revalidate nonce/payload shape immediately before `AEAD.Open`;
5. derive and authenticate-decrypt the raw bytes; and
6. check the committed SHA-256 plaintext hash.

Results are returned in original consensus order. Trying combinations tolerates
some malformed extra shares, but it can become combinatorially expensive when
many unverified candidates are admitted.

## Determinism And Invariants

- Ciphertexts are probabilistic, but their accepted binary encoding is
  canonical and fixes `BatchID`.
- Cluster, slot, and index are bound in AEAD and proof context.
- Production planning revalidates the scope stored by scoped decoding.
- No sub-batch may contain a repeated puncture index.
- Existing collision-free round-robin memberships remain unchanged; only
  formerly rejected collisions use the fallback.
- Every share is scoped to one `BatchID` and sub-batch.
- Plaintext results always return to the consensus-selected order.
- Caller mutation of source bytes, decoded copies, or AEAD fields cannot change
  immutable decoded identity; mutated public plans must still fail without an
  AEAD panic.

## Validation And Failure Semantics

- Structural, version, scope, context, point/scalar, length, AEAD-shape, and
  trailing-byte errors are returned, not filtered.
- Proof failure occurs during `MakeShare` and lower-level combine.
- A selected malformed ciphertext therefore prevents share generation or
  planning for the entire batch in the active application.
- Wrong-batch and malformed shares are errors at the library boundary. The node
  prefilters wrong-batch shares and retries combine only after new candidates
  arrive.
- An empty decoded batch is valid as an ownership object but cannot produce a
  `BatchPlan`.

## Concurrency And Ownership

The cluster-facing library does not provide internal synchronization. A node
uses it serially for encryption requests through its input lock, share
generation within one ACS-output handler, and combination through a node-level
single-flight claim. Independent BTE objects may be used by independent nodes.

Kyber objects are mutable interfaces. `DecodedBatch` explicitly clones them
when returning public ciphertexts, but `BatchPlan`, `SecretShare`, and
`DecryptionShare` remain public mutable structures. Callers must not mutate a
plan being used concurrently.

## Paper Correspondence And Deviations

The lower-level construction follows
[BEAT-MEV: Epochless Approach to Batched Threshold Encryption for MEV Prevention](/bloc/papers/BEAT-MEV.pdf):

- `PRFSetup`/`KeyGen` correspond to setup and threshold-key generation;
- `EncWithContext` corresponds to indexed BTE encryption plus proof;
- `VerifyCT` checks the ciphertext proof;
- `BatchDecWithShare` verifies/aggregates a batch and emits one partial share;
- `BatchCombineMessages` reconstructs the batch messages; and
- `alpha = 2*sqrt(B)` corresponds to the paper's Opt-2 sub-batching experiment.

The paper's collision distribution motivates raising `alpha` to maximum index
frequency and deterministically separating repeated indices. The repository's
least-loaded fallback is an implementation repair that preserves the original
round-robin layout whenever it is already valid.

Implementation-specific adaptations and deviations are:

- source groups are swapped for BLS12-381 performance;
- raw bytes use a new AES-GCM hybrid envelope around the `GT` message;
- cluster/slot/index context is added to AEAD and the proof transcript;
- deterministic binary encodings and `BatchID` are repository interfaces;
- the integrated API fixes Opt-2 instead of exposing normal/Opt-1/parallel
  choices;
- setup uses a serialized public artifact but still includes insecure diagonal
  elements; and
- key generation/configuration is a trusted-dealer prototype without DKG.

## Test Evidence

`be/cluster_test.go` covers:

- lower-level message recovery and proof mutation rejection;
- hybrid round trips and arbitrary valid threshold subsets;
- wrong/invalid extra candidates;
- canonical serialization and decode/plan equivalence;
- scoped cluster/slot rejection;
- oversized, unsupported-version, trailing, malformed point/scalar, nonce, and
  payload errors;
- immutable `DecodedBatch` identity and deep copies;
- deterministic planning, repeated-index separation, collision fallback,
  preserved golden memberships, and varied frequency properties;
- threshold and metadata enforcement; and
- canonical/malformed decoder fuzz seeds.

`elgamal/elgamal_test.go` covers threshold ElGamal. The inherited root benchmark
file covers normal and optimization comparison surfaces; cluster benchmarks
cover the hybrid full path and planning attribution.

Run `go test ./...` from `bte/btd-impl-main`. Benchmark and fuzz commands remain
in [TESTING.md](/bloc/bte/btd-impl-main/TESTING.md).

## Known Limitations

- The public CRS no longer exposes setup scalars, but still includes elements
  explicitly marked insecure for real setup.
- Setup and threshold shares still come from one trusted generator rather than
  an auditable MPC ceremony and DKG.
- Decryption shares have no public correctness proof or attribution mechanism.
- Combination may enumerate a large number of threshold subsets when invalid
  extras are admitted.
- Public mutable plans and shares receive only partial defensive validation;
  generic callers must respect constructor invariants.
- There is no DKG, proactive resharing, committee rotation, secure deletion,
  stable cross-language wire commitment, or independent cryptographic audit.
- `SuiteID` is metadata only and is not negotiated in ciphertext encodings.
- Detailed confirmed findings and follow-up priorities are in the
  [implementation review](/bloc/docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).
