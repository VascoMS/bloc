# Reconnectable Final-Campaign Remote Jobs

## Objective

Remove the issue #15 evaluator's dependence on one long-lived SSH channel
without changing frozen source `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`,
either image digest, corpus, evaluator arguments, balanced schedule, metric
schema, topology, or artifact acceptance rules.

## Boundary

SSH remains the short-lived controller transport for staging, idempotent job
start, status polling, and artifact recovery. The evaluator itself runs as a
detached controller-local job. Prometheus, evaluator CSV/JSON output, resource
sampling, and operator Compose logs remain the measurement and diagnostic data
sources.

## Remote Job State

Each evaluator invocation has the deterministic identity
`<experiment>-block-<block>-batch-<batch>-slot-<first-slot>` under
`/opt/bloc/ec2/jobs/`. A controller-side helper atomically claims that identity
by creating its directory. Only the process that creates the directory may
write the command and launch it. A repeated start returns the existing state
and never launches another process.

The job directory contains the quoted command, PID, stdout, stderr, and an
atomically published integer exit-status file. Status is one of `RUNNING`,
`EXIT:<code>`, `MISSING`, `AMBIGUOUS`, or `LOST`. Missing PID after a claimed
directory and a dead PID without an exit file fail closed; neither state is
automatically restarted.

## Controller Flow

For each existing block/batch invocation, the lifecycle wrapper:

1. sends the same `docker run ... eval-remote` arguments to the helper;
2. retries the idempotent short start request at most three times;
3. polls status through independent SSH connections every ten seconds for at
   most 180 polls;
4. accepts only `EXIT:0`, rejects a nonzero or ambiguous terminal state, and
   tolerates transient polling connection failures within the overall bound;
5. advances `next_slot` only after `EXIT:0`; and
6. recovers job stdout/stderr/state with the existing evaluator artifacts.

An SSH response may be lost after launch, but the repeated start observes the
same claimed identity. It cannot launch the evaluator twice. Exhausting the
poll bound invalidates the phase and triggers the existing recovery and cleanup
path.

## Validation

Behavior-level shell regressions must prove: duplicate start executes once;
lost start response reconnects without re-execution; transient status failures
recover; nonzero, ambiguous, lost, and exhausted jobs fail closed; existing
slot allocation and fail-fast measurement behavior remain intact; job files
are staged/recovered; and the complete lifecycle, contract, artifact, runner,
and validate-only suites pass on the task branch and byte-identical frozen
overlay.

Live p4 remains separately authorized after local validation. The invalid p3
rows are never merged into p4.
