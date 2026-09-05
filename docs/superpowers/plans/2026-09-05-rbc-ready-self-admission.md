# RBC READY Self-Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make both RBC READY-emission paths admit the local READY exactly once so an `n=4,f=1` node with two matching ECHOs and two matching remote READYs can reconstruct without waiting for a third remote READY.

**Architecture:** Replace the duplicated ECHO-quorum and READY-relay emission blocks with one `emitReady(root []byte) error` transition owned by `RBC`. The transition records the local READY before it queues the outbound broadcast, then immediately retries decoding; all existing root binding, distinct-sender thresholds, and post-reconstruction Merkle verification remain unchanged.

**Tech Stack:** Go 1.x, `github.com/klauspost/reedsolomon`, `github.com/stretchr/testify`, existing `sbc/hbbft` ACS safety campaign.

**Spec:** `docs/superpowers/specs/2026-09-02-rbc-ready-stream-lanes-design.md`

## Global Constraints

- Before implementation, create one repository issue named `Fix RBC READY relay self-admission`, add it to the BLOC Thesis Prototype project, assign milestone `M5. Performance, Scaling, And Resource Evidence`, and set Project fields `Roadmap target=M5`, `Status=In progress`, `Priority=High`, and `Area=ACS`.
- Execute in a fresh worktree created with `superpowers:using-git-worktrees` and a short-lived branch named `codex/rbc-ready-self-admission`.
- Preserve the user's uncommitted `papers/ACS_Improvement.pdf`; do not stage, rewrite, or remove it.
- Keep the RBC thresholds exactly `N-F` ECHOs, `F+1` READYs for relay, and `2F+1` READYs plus `F+1` ECHOs for decoding.
- Count the local READY exactly once in `recvReadys`, emit exactly one outbound `ReadyRequest`, and do not recurse through `handleReadyRequest` to do self-admission.
- Keep mixed-root filtering, distinct sender accounting, retryable reconstruction, and the reconstructed Merkle-root equality check unchanged.
- Do not change protobuf, JSON, transport, evaluator, or trace schemas in this plan.
- Do not run cloud infrastructure or campaign commands; this plan is fully local.
- End by checking branch, worktree status, and `git rev-list --left-right --count main...HEAD`, posting validation evidence to the issue, and reporting whether `docs/STATUS.md` required an update.

---

### Task 1: Reproduce and fix READY-relay self-admission

**Files:**
- Modify: `sbc/hbbft/rbc.go:273-321`
- Test: `sbc/hbbft/rbc_test.go:15-58`

**Interfaces:**
- Consumes: `makeShards(reedsolomon.Encoder, []byte) ([][]byte, error)`, `makeProofRequests([][]byte) ([]*ProofRequest, error)`, `RBC.HandleMessage(uint64, *BroadcastMessage) error`, and `RBC.tryDecodeValue([]byte) error`.
- Produces: `func (r *RBC) emitReady(root []byte) error`; both `handleEchoRequest` and `handleReadyRequest` call this method.

- [ ] **Step 1: Add a deterministic READY-relay fixture helper**

Add this helper near the top of `sbc/hbbft/rbc_test.go`, after `TestRBCTraceRecordsThresholdAndReconstructionMilestones`:

```go
func readyRelayFixture(t *testing.T) (*RBC, []byte, []*ProofRequest) {
	t.Helper()
	rbc := NewRBC(Config{ID: 0, N: 4, F: 1, Nodes: makeids(4)}, 0)
	value := []byte("traceable-rbc-payload!")
	shards, err := makeShards(rbc.enc, value)
	require.NoError(t, err)
	proofs, err := makeProofRequests(shards)
	require.NoError(t, err)
	return rbc, value, proofs
}

func handleRBCEcho(t *testing.T, rbc *RBC, senderID uint64, proof *ProofRequest) {
	t.Helper()
	require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
		Payload: &EchoRequest{ProofRequest: *proof},
	}))
}

func handleRBCReady(t *testing.T, rbc *RBC, senderID uint64, root []byte) {
	t.Helper()
	require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
		Payload: &ReadyRequest{RootHash: root},
	}))
}
```

- [ ] **Step 2: Write the failing liveness regression**

Add this test below the fixture:

```go
func TestRBCReadyRelayAdmitsLocalReady(t *testing.T) {
	rbc, value, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	// Two distinct ECHOs provide the N-2F shards needed for reconstruction,
	// but remain below the N-F ECHO threshold that directly emits READY.
	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCEcho(t, rbc, 2, proofs[2])
	require.Empty(t, rbc.Messages())

	handleRBCReady(t, rbc, 1, root)
	handleRBCReady(t, rbc, 2, root)

	assert.Equal(t, 3, rbc.countReadys(root))
	assert.Equal(t, root, rbc.recvReadys[rbc.ID])
	require.Equal(t, value, rbc.Output())

	messages := rbc.Messages()
	require.Len(t, messages, 1)
	ready, ok := messages[0].Payload.(*ReadyRequest)
	require.True(t, ok)
	assert.Equal(t, root, ready.RootHash)
}
```

- [ ] **Step 3: Add the decode-evidence and exactly-once regressions**

Add both tests:

```go
func TestRBCReadyRelayStillWaitsForEnoughMatchingEchos(t *testing.T) {
	rbc, value, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCReady(t, rbc, 1, root)
	handleRBCReady(t, rbc, 2, root)

	assert.Equal(t, 3, rbc.countReadys(root))
	assert.Nil(t, rbc.Output())
	require.Len(t, rbc.Messages(), 1)

	// Reconstruction becomes eligible when a second matching shard arrives.
	handleRBCEcho(t, rbc, 2, proofs[2])
	require.Equal(t, value, rbc.Output())
	assert.Empty(t, rbc.Messages())
}

func TestRBCReadyEmissionRemainsExactlyOnceAfterEchoQuorum(t *testing.T) {
	rbc, _, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	handleRBCReady(t, rbc, 1, root)
	handleRBCReady(t, rbc, 2, root)
	require.Len(t, rbc.Messages(), 1)

	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCEcho(t, rbc, 2, proofs[2])
	handleRBCEcho(t, rbc, 3, proofs[3])

	assert.Empty(t, rbc.Messages())
	assert.Equal(t, root, rbc.recvReadys[rbc.ID])
	assert.Equal(t, 3, rbc.countReadys(root))
}
```

- [ ] **Step 4: Run the focused tests and confirm the current bug**

Run:

```bash
cd sbc/hbbft
go test ./... -run 'TestRBCReady(RelayAdmitsLocalReady|RelayStillWaitsForEnoughMatchingEchos|EmissionRemainsExactlyOnceAfterEchoQuorum)$' -count=1
```

Expected before the fix: `TestRBCReadyRelayAdmitsLocalReady` fails because `countReadys(root)` is `2` and `Output()` is `nil`. The other two tests may also fail at the expected local-count assertion; there must be no panic or unrelated compilation failure.

- [ ] **Step 5: Implement one non-recursive READY transition**

Add this method immediately before `handleReadyRequest` in `sbc/hbbft/rbc.go`:

```go
func (r *RBC) emitReady(root []byte) error {
	if r.readySent {
		return r.tryDecodeValue(root)
	}
	if _, exists := r.recvReadys[r.ID]; exists {
		return fmt.Errorf("local ready already admitted for node %d", r.ID)
	}

	r.readySent = true
	r.recvReadys[r.ID] = root
	r.trace.recordRBC(r.proposerID, traceRBCReadySent)
	r.messages = append(r.messages, &BroadcastMessage{
		Payload: &ReadyRequest{RootHash: root},
	})
	return r.tryDecodeValue(root)
}
```

Replace the ECHO-quorum emission block at `rbc.go:295-299` with:

```go
	return r.emitReady(req.RootHash)
```

Replace the READY-relay block at `rbc.go:314-320` with:

```go
	if r.countReadys(req.RootHash) == r.F+1 && !r.readySent {
		return r.emitReady(req.RootHash)
	}
	return r.tryDecodeValue(req.RootHash)
```

The helper must store `recvReadys[r.ID]` before calling `tryDecodeValue`; otherwise the local READY cannot satisfy the `2F+1` threshold in the same transition.

- [ ] **Step 6: Run the focused tests and existing root-binding regressions**

Run:

```bash
cd sbc/hbbft
go test ./... -run 'TestRBC(Ready|RejectsMixedRootReconstruction|RejectsReconstructionWithWrongRoot|TraceRecordsThresholdAndReconstructionMilestones)' -count=1
```

Expected: PASS. `TestRBCTraceRecordsThresholdAndReconstructionMilestones` must still observe one `ReadySent` point, and both root-reconstruction tests must remain green.

- [ ] **Step 7: Commit the protocol fix**

```bash
git add sbc/hbbft/rbc.go sbc/hbbft/rbc_test.go
git commit -m "fix(hbbft): admit local READY on relay"
```

### Task 2: Validate the RBC/ACS safety boundary and update canonical state

**Files:**
- Modify: `docs/modules/hbbft.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Consumes: the fixed `RBC.emitReady(root []byte) error` transition from Task 1 and `bloc-node/scripts/run-acs-safety-campaign.sh`.
- Produces: canonical documentation of the self-admission invariant and local validation evidence suitable for the task issue.

- [ ] **Step 1: Correct the RBC algorithm description**

In `docs/modules/hbbft.md`, replace the READY transition text in stage 2 with this exact behavior:

```markdown
For a matching root:

- `N-F` ECHOs cause a node that has not sent READY to admit its own READY and
  broadcast READY.
- `F+1` READYs cause a node that has not sent READY to admit its own READY and
  relay READY.
- Both emission paths count the local READY exactly once before retrying
  decoding; neither path waits for the node's broadcast to loop back through
  the transport.
- `2F+1` READYs plus `F+1` ECHOs make the implementation attempt
  reconstruction.
```

Add this invariant under `## Determinism And Invariants`:

```markdown
- A local READY is admitted exactly once before its corresponding broadcast is
  exposed to the transport.
```

- [ ] **Step 2: Document the focused validation contract**

In the ACS safety section of `docs/VALIDATION.md`, add:

```markdown
The RBC READY-relay regression uses `n=4,f=1`, two distinct matching ECHOs,
and two matching remote READYs. Passing requires the local READY to become the
third distinct READY, one READY broadcast to be queued, and the value to decode
without a third remote READY. Companion tests prove that READY quorum alone
cannot decode without `F+1` matching ECHOs and that later ECHO quorum does not
emit a duplicate READY.
```

- [ ] **Step 3: Run the module suite and focused race checks**

Run:

```bash
cd sbc/hbbft
go test ./... -count=1
go test -race ./... -run 'Test(RBCReady|RBCRejectsMixedRoot|SlotACS)' -count=1
```

Expected: both commands exit `0`; all packages report `ok` or `[no test files]`, with no race report.

- [ ] **Step 4: Run the repository ACS safety campaign**

Run from the repository root:

```bash
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

Expected: exit `0`; the script reports all deterministic delivery schedules, the sustained gate, and compatibility cases passing. Retain generated evaluator output only in the script's ignored `results/` destination.

- [ ] **Step 5: Record the implementation and resolve the live risk**

Append this dated entry under the current release section of `docs/CHANGELOG.md`:

```markdown
- Fixed RBC READY relay self-admission by routing both READY triggers through
  one exactly-once local transition; added direct `n=4,f=1` liveness,
  decode-evidence, and duplicate-emission regressions and reran the complete ACS
  safety campaign.
```

In `docs/STATUS.md`:

1. Set `Last reviewed` to the execution date.
2. Remove the open-risk bullet headed `RBC READY relay omits local self-admission`.
3. Leave the trace-finalization risk open.
4. In `Immediate Actions`, remove the READY implementation action and make trace finalization the first action.
5. Do not change the active milestone or last-known-good baseline unless this task is separately authorized to accept a new baseline.

- [ ] **Step 6: Check documentation, focused diffs, and repository state**

Run:

```bash
git diff --check
git diff -- docs/modules/hbbft.md docs/VALIDATION.md docs/CHANGELOG.md docs/STATUS.md
git status --short --branch
git rev-list --left-right --count main...HEAD
```

Expected: no whitespace errors; only this task's four documentation files are uncommitted; `papers/ACS_Improvement.pdf` is not staged; the task branch is ahead of `main` and not behind the remote base selected for the worktree.

- [ ] **Step 7: Commit documentation and evidence state**

```bash
git add docs/modules/hbbft.md docs/VALIDATION.md docs/CHANGELOG.md docs/STATUS.md
git commit -m "docs: record RBC READY self-admission fix"
```

- [ ] **Step 8: Post issue evidence and perform the final gate**

Post the exact commands and pass/fail outcomes from Steps 3 and 4 to `Fix RBC READY relay self-admission`. Then run:

```bash
git status --short --branch
git log -2 --oneline
git rev-list --left-right --count main...HEAD
```

Expected: the two task commits are present; no task-owned files remain modified; any `papers/ACS_Improvement.pdf` change is still the user's unstaged change. Close the issue only after its acceptance and validation sections are satisfied; otherwise leave it in progress with the concrete blocker.
