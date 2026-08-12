# Final Campaign Image-Pull Retry Design

## Objective

Allow issue #15's final campaign lifecycle to tolerate a short SSH interruption
during immutable image distribution while continuing to fail closed on any
unverified host or image.

This is a campaign-tooling-only correction. It does not change frozen source
`cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`, either ECR image digest, the n4/n7
bundles, corpus, schema, topology, schedule, evaluator arguments, configuration,
or protocol semantics.

## Evidence And Failure Boundary

The rejected n7 same-AZ p5 attempt reached every staging gate. Its controller
then stopped answering SSH during the final digest-pinned image operation and
was reachable again during recovery about one minute later. No service or
measurement started. The current `final_pull_one_image` makes one bounded SSH
call, while `final_pull_verify_images` does not explicitly return after each
operator image failure. Because the lifecycle invokes that function in a Bash
conditional, a later successful loop command can mask an earlier failure.

## Considered Approaches

1. Retry each exact image operation independently, at most three times, and
   stop immediately when an image exhausts its attempts. This is the selected
   approach. Docker pull by immutable digest is idempotent, already downloaded
   layers are reusable, and every attempt repeats the existing digest and
   `linux/amd64` checks.
2. Retry both operator images as one host-level unit. This is safe but repeats a
   successful BLOC pull when only the mempool pull failed and makes the failure
   boundary less precise.
3. Relaunch unchanged. This preserves the one-shot behavior and known masking
   defect, so another full topology can fail on the same transient boundary or,
   worse, continue after an earlier masked failure.

## Lifecycle Behavior

`final_pull_one_image` will call the existing bounded `final_ssh` command up to
three times. It returns immediately after success. After the first and second
failures it waits two seconds, matching the existing bounded SCP recovery
pattern. After the third failure it returns nonzero without further delay.

The remote command remains unchanged: authenticate to the region encoded in the
private-ECR reference, pull that full digest reference, verify the local image
architecture is `amd64`, and require the requested repository digest in
`RepoDigests`. There is no tag fallback, alternate registry, rebuild,
substitution, or acceptance based only on a successful SSH connection.

`final_pull_verify_images` will append explicit `|| return 1` propagation to
both image operations on every operator and the BLOC image operation on the
controller. It therefore stops at the first exhausted image and cannot let a
later host mask that failure. Services remain gated on the complete image phase.

## Tests

The lifecycle regression will prove three independent behaviors before the
implementation is added:

- two transient SSH failures followed by success make exactly three attempts,
  two sleeps, and still execute the unchanged digest and architecture checks;
- three failures return nonzero after exactly three attempts and two sleeps;
- a failure on the first operator image returns immediately, without attempting
  that operator's second image, any later operator, or the controller.

The focused test must fail against the current helper for the expected missing
retry/fail-fast behavior before production code changes. After implementation,
the focused lifecycle suite, same-AZ and three-region adapter variants, final
campaign contract, artifact tests, runner portability, Bash syntax, diff
hygiene, Terraform validation, and exact n7 p6 `--validate-only` contract must
pass without AWS access.

## Frozen Overlay And Documentation

After local validation, only the changed lifecycle helper is copied into the
detached frozen execution worktree and its SHA-256 is compared byte-for-byte
with the task-branch file. The canonical EC2 runbook and validation contract
will document the bounded image-pull behavior. `STATUS.md`, `CHANGELOG.md`, and
issue #15 will record the resolved blocker and the next separate live
authorization gate.

No AWS API, Terraform plan/apply, ECR write, EC2 creation, Git push, or p6 live
execution is part of this correction.
